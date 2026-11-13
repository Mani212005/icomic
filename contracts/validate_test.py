"""Contract validation tests for Icomic OpenAPI and JSON schemas."""

import json
from pathlib import Path
import unittest
import jsonschema
import yaml

CONTRACTS_DIR = Path(__file__).resolve().parent


class TestContracts(unittest.TestCase):
    """Verifies that all contracts and schemas are valid and conformant."""

    def test_openapi_spec_structure(self):
        spec_path = CONTRACTS_DIR / "openapi" / "ai-service.yaml"
        self.assertTrue(spec_path.exists(), f"Missing spec at {spec_path}")

        with open(spec_path, "r", encoding="utf-8") as f:
            spec = yaml.safe_load(f)

        self.assertIn("openapi", spec)
        self.assertTrue(spec["openapi"].startswith("3.1"))
        self.assertIn("info", spec)
        self.assertEqual(spec["info"]["title"], "Icomic AI Service Internal API")
        self.assertIn("paths", spec)

        # Ensure all key required endpoints exist
        required_paths = [
            "/healthz",
            "/internal/v1/analyze-script",
            "/internal/v1/generate-character-reference",
            "/internal/v1/generate-chapter-outline",
            "/internal/v1/generate-panel-text",
            "/internal/v1/generate-draft-sketch",
            "/internal/v1/generate-line-art",
            "/internal/v1/suggest-colour-palette",
            "/internal/v1/moderate-prompt",
            "/internal/v1/assemble-pdf",
            "/internal/v1/job/{id}/status",
        ]
        for path in required_paths:
            self.assertIn(path, spec["paths"], f"Expected path {path} in OpenAPI spec")

    def test_account_error_codes_in_go_match_the_contract(self):
        """Every stable account error code the Go API emits must be listed in the contract."""
        import re

        spec = yaml.safe_load((CONTRACTS_DIR / "openapi/ai-service.yaml").read_text())
        schemas = spec["components"]["schemas"]
        documented = set(schemas["AuthErrorResponse"]["properties"]["code"]["enum"])
        backend = CONTRACTS_DIR.parent / "backend/internal"
        handlers = (backend / "api/accounts.go").read_text()
        service = (backend / "accounts/service.go").read_text()
        emitted = set(re.findall(r'writeCodedError\(w, [^,]+, "([A-Za-z_]+)"', handlers))
        emitted |= set(re.findall(r'Code: "([A-Za-z_]+)"', handlers))
        emitted |= set(re.findall(r'(?:RateLimitedCode|InvalidCredentialCode)\s*=\s*"([a-z_]+)"', service))
        emitted.discard("project_limit_reached")
        self.assertTrue(emitted, "expected to find account error codes in the Go source")
        self.assertEqual(emitted - documented, set(), "Go emits codes missing from AuthErrorResponse")
        limit_codes = set(schemas["ProjectLimitError"]["properties"]["code"]["enum"])
        domain = (backend / "domain/accounts.go").read_text()
        self.assertIn('ProjectLimitReachedCode = "project_limit_reached"', domain)
        self.assertIn("project_limit_reached", limit_codes)

    def test_lettering_schema_matches_openapi_and_validates_fixtures(self):
        schema = json.loads((CONTRACTS_DIR / "schemas/lettering.schema.json").read_text())
        jsonschema.Draft202012Validator.check_schema(schema)
        validator = jsonschema.Draft202012Validator(schema)
        spec = yaml.safe_load((CONTRACTS_DIR / "openapi/ai-service.yaml").read_text())
        expected = {
            name: definition for name, definition in spec["components"]["schemas"].items()
            if name.startswith("Lettering")
        }
        # The standalone schema must not drift from the contract-first definitions.
        actual = json.loads(json.dumps(schema["$defs"]).replace("#/$defs/", "#/components/schemas/"))
        self.assertEqual(expected, actual)
        fixtures = CONTRACTS_DIR.parent / "ai/tests/fixtures/lettering"
        for path in sorted(fixtures.glob("*.json")):
            with self.subTest(path=path.name):
                validator.validate(json.loads(path.read_text()))
        valid = json.loads((fixtures / "speech-tail.input.json").read_text())
        for key, value in [("art_mode", "unknown"), ("image_url", "not-an-input")]:
            invalid = {**valid, key: value}
            with self.assertRaises(jsonschema.ValidationError):
                validator.validate(invalid)
        invalid = json.loads(json.dumps(valid))
        invalid["panels"][0]["rect"]["x"] = 1.1
        with self.assertRaises(jsonschema.ValidationError):
            validator.validate(invalid)

    def test_public_lettering_contract_and_patch_payloads(self):
        public = yaml.safe_load((CONTRACTS_DIR / "openapi/lettering-api.yaml").read_text())
        self.assertIn("get", public["paths"]["/api/v1/pages/{id}/lettering"])
        self.assertIn("patch", public["paths"]["/api/v1/pages/{id}/lettering/utterances/{utterance_id}/override"])
        self.assertIn("patch", public["paths"]["/api/v1/pages/{id}/lettering/zones"])
        spec = yaml.safe_load((CONTRACTS_DIR / "openapi/ai-service.yaml").read_text())
        schema = {"$ref": "#/components/schemas/PatchLetteringOverrideRequest", "components": spec["components"]}
        validator = jsonschema.Draft202012Validator(schema)
        validator.validate({"override": {}})
        validator.validate({"override": {"center": {"x": .5, "y": .2}}})
        for invalid in [{}, {"override": {"size_scale": 9}}, {"override": {"center": {"x": .5}}}]:
            with self.assertRaises(jsonschema.ValidationError):
                validator.validate(invalid)

    def test_public_generation_contract_covers_every_receipt_route(self):
        """Every paid generation route the Go router serves answers a 202 receipt, and says so here."""
        import re

        public = yaml.safe_load((CONTRACTS_DIR / "openapi/generation-api.yaml").read_text())
        router = (CONTRACTS_DIR.parent / "backend/internal/api/router.go").read_text()
        served = set(
            re.findall(
                r'handle\("POST (/api/v1/[^"]+)", s\.(?:GenerateCharacterReference|GenerateDraft|GenerateLineArt|GeneratePaintedPage|SuggestColourPalette)\)',
                router,
            )
        )
        self.assertEqual(len(served), 5, "expected the five debited generation routes in the router")
        self.assertEqual(served - set(public["paths"]), set(), "generation routes missing from generation-api.yaml")
        for path in served:
            responses = public["paths"][path]["post"]["responses"]
            self.assertIn("202", responses, f"{path} must document its receipt")
            self.assertNotIn("200", responses, f"{path} no longer returns a completed result")
            self.assertEqual(responses["202"], {"$ref": "#/components/responses/Receipt"})
        self.assertIn("get", public["paths"]["/api/v1/generation-jobs/{id}"])

    def test_cancel_route_is_documented_and_its_answer_is_honest(self):
        """The router serves POST /generation-jobs/{id}/cancel; the contract says the three outcomes."""
        public = yaml.safe_load((CONTRACTS_DIR / "openapi/generation-api.yaml").read_text())
        router = (CONTRACTS_DIR.parent / "backend/internal/api/router.go").read_text()
        self.assertIn('handle("POST /api/v1/generation-jobs/{id}/cancel", s.CancelJob)', router)
        cancel = public["paths"]["/api/v1/generation-jobs/{id}/cancel"]["post"]
        self.assertEqual(
            cancel["responses"]["200"]["content"]["application/json"]["schema"],
            {"$ref": "./ai-service.yaml#/components/schemas/CancelJobResponse"},
        )
        for status in ("403", "404"):
            self.assertIn(status, cancel["responses"])
        spec = yaml.safe_load((CONTRACTS_DIR / "openapi/ai-service.yaml").read_text())
        schema = spec["components"]["schemas"]["CancelJobResponse"]
        self.assertEqual(schema["properties"]["outcome"]["enum"], ["cancelled", "settling", "finished"])
        self.assertEqual(set(schema["required"]), {"outcome", "job", "refunded", "message"})

    def test_generation_receipt_schema_accepts_a_receipt_and_rejects_a_result(self):
        spec = yaml.safe_load((CONTRACTS_DIR / "openapi/ai-service.yaml").read_text())
        validator = jsonschema.Draft202012Validator(
            {"$ref": "#/components/schemas/GenerationReceipt", "components": spec["components"]}
        )
        job = {
            "id": "11111111-1111-1111-1111-111111111111",
            "project_id": "22222222-2222-2222-2222-222222222222",
            "user_id": "33333333-3333-3333-3333-333333333333",
            "job_type": "draft_sketch",
            "stage": "draft",
            "model_id": "muse-image-1.0",
            "status": "pending",
            "idempotency_key": "key",
            "estimated_cost": 0.01,
            "hashed_user_id": "hash",
            "created_at": "2026-01-01T00:00:00Z",
        }
        receipt = {
            "job": job,
            "job_id": job["id"],
            "status": "pending",
            "entity_type": "page",
            "entity_id": "44444444-4444-4444-4444-444444444444",
            "project_id": job["project_id"],
        }
        validator.validate(receipt)
        validator.validate({**receipt, "variant": "direct-from-brief"})
        for invalid in [
            {key: value for key, value in receipt.items() if key != "job_id"},
            {**receipt, "entity_type": "project"},
            {**receipt, "status": "finished"},
        ]:
            with self.assertRaises(jsonschema.ValidationError):
                validator.validate(invalid)

    def test_job_schema(self):
        schema_path = CONTRACTS_DIR / "schemas" / "job.schema.json"
        self.assertTrue(schema_path.exists(), f"Missing schema at {schema_path}")

        with open(schema_path, "r", encoding="utf-8") as f:
            schema = json.load(f)

        # Validate schema itself
        validator = jsonschema.Draft202012Validator(schema)
        validator.check_schema(schema)

        # Valid instance
        valid_job = {
            "id": "11111111-1111-1111-1111-111111111111",
            "project_id": "22222222-2222-2222-2222-222222222222",
            "user_id": "33333333-3333-3333-3333-333333333333",
            "job_type": "draft_sketch",
            "stage": "draft",
            "model_id": "muse-image-1.0",
            "status": "pending",
            "idempotency_key": "job-key-12345",
            "estimated_cost": 0.01,
            "actual_cost": None,
            "hashed_user_id": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
            "error_message": None,
            "created_at": "2026-11-01T12:00:00Z",
            "completed_at": None,
        }
        validator.validate(valid_job)

        # Invalid instance (missing required job_type)
        invalid_job = dict(valid_job)
        del invalid_job["job_type"]
        with self.assertRaises(jsonschema.ValidationError):
            validator.validate(invalid_job)

    def test_credit_schema(self):
        schema_path = CONTRACTS_DIR / "schemas" / "credit.schema.json"
        self.assertTrue(schema_path.exists(), f"Missing schema at {schema_path}")

        with open(schema_path, "r", encoding="utf-8") as f:
            schema = json.load(f)

        # Validate schema itself
        validator = jsonschema.Draft202012Validator(schema)
        validator.check_schema(schema)

        # Valid instance: debit with generation_job_id
        valid_credit = {
            "id": "44444444-4444-4444-4444-444444444444",
            "user_id": "33333333-3333-3333-3333-333333333333",
            "project_id": "22222222-2222-2222-2222-222222222222",
            "generation_job_id": "11111111-1111-1111-1111-111111111111",
            "transaction_type": "debit",
            "amount": 0.01,
            "balance_after": 9.99,
            "created_at": "2026-11-01T12:00:00Z",
        }
        validator.validate(valid_credit)

        # Valid instance: adjustment / signup credit with null generation_job_id and null project_id
        valid_adjustment = {
            "id": "55555555-5555-5555-5555-555555555555",
            "user_id": "33333333-3333-3333-3333-333333333333",
            "project_id": None,
            "generation_job_id": None,
            "transaction_type": "adjustment",
            "amount": 100.0,
            "balance_after": 100.0,
            "created_at": "2026-11-01T12:00:00Z",
        }
        validator.validate(valid_adjustment)

        # Valid instance: omitted nullable fields
        valid_omitted = {
            "id": "66666666-6666-6666-6666-666666666666",
            "user_id": "33333333-3333-3333-3333-333333333333",
            "transaction_type": "adjustment",
            "amount": 50.0,
            "balance_after": 150.0,
            "created_at": "2026-11-01T12:00:00Z",
        }
        validator.validate(valid_omitted)

        # Invalid transaction type
        invalid_credit = dict(valid_credit)
        invalid_credit["transaction_type"] = "invalid_type"
        with self.assertRaises(jsonschema.ValidationError):
            validator.validate(invalid_credit)

        # Invalid generation_job_id type (numeric instead of uuid/null)
        invalid_job_id_credit = dict(valid_credit)
        invalid_job_id_credit["generation_job_id"] = 12345
        with self.assertRaises(jsonschema.ValidationError):
            validator.validate(invalid_job_id_credit)


if __name__ == "__main__":
    unittest.main()
