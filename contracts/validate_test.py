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

        # Valid instance
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

        # Invalid transaction type
        invalid_credit = dict(valid_credit)
        invalid_credit["transaction_type"] = "invalid_type"
        with self.assertRaises(jsonschema.ValidationError):
            validator.validate(invalid_credit)


if __name__ == "__main__":
    unittest.main()
