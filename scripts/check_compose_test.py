"""Hermetic tests for the Compose invariant checker (no docker needed)."""

from __future__ import annotations

import copy
import unittest

from check_compose import check_base, check_e2e_overlay, check_prod_overlay

GOOD = {
    "volumes": {"ai_data": {}},
    "services": {
        "ai": {
            "environment": {
                "ADAPTER_STORAGE": "local",
                "LOCAL_STORAGE_DIR": "/var/lib/icomic/storage",
                "PROVIDER_COST_LEDGER_PATH": "/var/lib/icomic/provider-costs.json",
                "PROVIDER_SPEND_CAP_USD": "2",
                "LETTERING_OVERLAY_ENABLED": "true",
            },
            "volumes": [{"type": "volume", "source": "ai_data", "target": "/var/lib/icomic"}],
            "healthcheck": {"test": ["CMD-SHELL", "curl http://127.0.0.1:8000/readyz"]},
        },
        "backend": {
            "environment": {"LETTERING_OVERLAY_ENABLED": "true"},
            "healthcheck": {"test": ["CMD-SHELL", "wget http://127.0.0.1:8080/readyz"]},
            "depends_on": {
                "ai": {"condition": "service_healthy"},
                "postgres": {"condition": "service_healthy"},
            },
        },
    },
}


def mutated(change) -> dict:
    config = copy.deepcopy(GOOD)
    change(config)
    return config


class TestComposeInvariants(unittest.TestCase):
    def test_good_configuration_passes(self):
        self.assertEqual(check_base(GOOD), [])

    def test_in_memory_storage_is_rejected(self):
        bad = mutated(lambda c: c["services"]["ai"]["environment"].update(ADAPTER_STORAGE="fake"))
        self.assertTrue(any("ADAPTER_STORAGE=local" in p for p in check_base(bad)))

    def test_storage_or_ledger_outside_a_named_volume_is_rejected(self):
        bad = mutated(lambda c: c["services"]["ai"].update(volumes=[]))
        self.assertEqual(len([p for p in check_base(bad) if "named volume" in p]), 2)
        bind = mutated(lambda c: c["services"]["ai"]["volumes"][0].update(type="bind"))
        self.assertEqual(len([p for p in check_base(bind) if "named volume" in p]), 2)
        undeclared = mutated(lambda c: c.update(volumes={}))
        self.assertEqual(len([p for p in check_base(undeclared) if "named volume" in p]), 2)

    def test_overlay_lettering_must_default_on_for_both_services(self):
        bad = mutated(
            lambda c: c["services"]["backend"]["environment"].update(
                LETTERING_OVERLAY_ENABLED="false"
            )
        )
        self.assertEqual([p for p in check_base(bad) if "OVERLAY" in p], [
            "backend must default LETTERING_OVERLAY_ENABLED to true"
        ])

    def test_liveness_probe_is_not_readiness(self):
        bad = mutated(
            lambda c: c["services"]["ai"].update(
                healthcheck={"test": ["CMD-SHELL", "curl http://127.0.0.1:8000/healthz"]}
            )
        )
        self.assertTrue(any("/readyz" in p for p in check_base(bad)))

    def test_backend_must_wait_for_ready_dependencies(self):
        bad = mutated(lambda c: c["services"]["backend"]["depends_on"].pop("ai"))
        self.assertTrue(any("healthy (ready) ai" in p for p in check_base(bad)))

    def test_cap_must_have_a_default(self):
        bad = mutated(lambda c: c["services"]["ai"]["environment"].update(PROVIDER_SPEND_CAP_USD=""))
        self.assertTrue(any("PROVIDER_SPEND_CAP_USD" in p for p in check_base(bad)))

    def test_e2e_overlay_rejects_fixed_names_and_host_ports(self):
        loopback = {"host_ip": "127.0.0.1", "published": "0", "target": 8000}
        ok = {"services": {"ai": {"ports": [loopback]}, "redis": {}}}
        self.assertEqual(check_e2e_overlay(ok), [])
        named = {"services": {"ai": {"container_name": "icomic-ai"}}}
        self.assertEqual(len(check_e2e_overlay(named)), 1)
        fixed = {"services": {"ai": {"ports": [{"host_ip": "127.0.0.1", "published": "8000"}]}}}
        self.assertEqual(len(check_e2e_overlay(fixed)), 1)
        public = {"services": {"ai": {"ports": [{"published": "0", "target": 8000}]}}}
        self.assertEqual(len(check_e2e_overlay(public)), 1)

    def test_production_overlay_keeps_durable_storage(self):
        good = {"services": {"ai": {"environment": {"ADAPTER_STORAGE": "local"}}}}
        self.assertEqual(check_prod_overlay(good), [])
        bad = {"services": {"ai": {"environment": {"ADAPTER_STORAGE": "fake"}}}}
        self.assertEqual(len(check_prod_overlay(bad)), 1)


if __name__ == "__main__":
    unittest.main()
