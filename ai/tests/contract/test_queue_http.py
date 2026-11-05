"""Exercise delivery retries through the same HTTP boundary used by Go."""

from fastapi.testclient import TestClient

from app.main import create_app


def test_duplicate_image_delivery_returns_saved_result_without_generation():
    app = create_app()
    image = app.state.image_adapter
    original = image.generate
    calls = []

    def generate(*args, **kwargs):
        calls.append(1)
        return original(*args, **kwargs)

    image.generate = generate
    payload = {
        "job_id": "11111111-1111-4111-8111-111111111111",
        "hashed_user_id": "test-user",
        "project_id": "22222222-2222-4222-8222-222222222222",
        "character_id": "33333333-3333-4333-8333-333333333333",
        "character_name": "Maya",
        "prompt": "An explorer",
        "style": "classic-comic",
    }
    client = TestClient(app)
    first = client.post("/internal/v1/generate-character-reference", json=payload)
    second = client.post("/internal/v1/generate-character-reference", json=payload)
    assert first.status_code == second.status_code == 200
    assert first.json() == second.json()
    assert len(calls) == 1
    assert app.state.queue_adapter.acknowledged


def test_redis_delivery_result_survives_app_restart_and_ack_failure():
    from fakeredis import FakeRedis

    from app.adapters.factory import build_adapters_from_env
    from app.adapters.queue.redis import RedisQueueAdapter

    client = FakeRedis()
    adapters = build_adapters_from_env()
    adapters.queue = RedisQueueAdapter(client=client)
    calls = []
    original = adapters.language.complete

    def complete(*args, **kwargs):
        calls.append(1)
        return original(*args, **kwargs)

    adapters.language.complete = complete
    payload = {
        "job_id": "11111111-1111-4111-8111-222222222222",
        "hashed_user_id": "test-user",
        "prompt": "An explorer",
    }
    app = create_app(adapters)
    acked = []

    def broken_ack(envelope):
        acked.append(envelope)
        raise OSError("connection interrupted after result save")

    adapters.queue.ack = broken_ack
    with TestClient(app) as http:
        first = http.post("/internal/v1/moderate-prompt", json=payload)
        assert first.status_code == 200
    assert len(calls) == 1
    assert acked
    # Recreate both the queue adapter and app, retaining only Redis data.
    adapters.queue = RedisQueueAdapter(client=client)
    with TestClient(create_app(adapters)) as http:
        status = http.get(f"/internal/v1/job/{payload['job_id']}/status")
        assert status.json()["status"] == "done"
        second = http.post("/internal/v1/moderate-prompt", json=payload)
        assert first.json() == second.json()
        assert len(calls) == 1
        changed = {**payload, "prompt": "Changed request"}
        assert http.post("/internal/v1/moderate-prompt", json=changed).status_code == 409


def test_generation_failure_is_saved_and_not_reexecuted():
    app = create_app()
    calls = []

    def fail(*args, **kwargs):
        calls.append(1)
        raise ValueError("Generation rejected")

    app.state.image_adapter.generate = fail
    payload = {
        "job_id": "11111111-1111-4111-8111-333333333333",
        "hashed_user_id": "test-user",
        "project_id": "22222222-2222-4222-8222-222222222222",
        "character_id": "33333333-3333-4333-8333-333333333333",
        "character_name": "Maya",
        "prompt": "An explorer",
        "style": "classic-comic",
    }
    client = TestClient(app)
    for _ in range(2):
        response = client.post("/internal/v1/generate-character-reference", json=payload)
        assert response.status_code == 400
    assert len(calls) == 1
    status = client.get(f"/internal/v1/job/{payload['job_id']}/status")
    assert status.json()["status"] == "failed"
    assert status.json()["error_code"] == "INVALID_ARGUMENT"
    assert status.json()["error_message"] == "Review the request and try again."
    assert status.json()["retryable"] is False


def test_retryable_provider_error_uses_safe_shared_envelope():
    from app.adapters.meta_http import MetaRateLimitError

    app = create_app()
    calls = []

    def fail(*args, **kwargs):
        calls.append(1)
        raise MetaRateLimitError("raw provider body token=secret", retry_after=13)

    app.state.image_adapter.generate = fail
    payload = {
        "job_id": "11111111-1111-4111-8111-444444444444",
        "hashed_user_id": "test-user",
        "project_id": "22222222-2222-4222-8222-222222222222",
        "character_id": "33333333-3333-4333-8333-333333333333",
        "character_name": "Maya",
        "prompt": "An explorer",
        "style": "classic-comic",
    }
    client = TestClient(app)
    response = client.post("/internal/v1/generate-character-reference", json=payload)
    body = response.json()
    assert response.status_code == 429
    assert response.headers["retry-after"] == "13"
    assert body == {
        "code": "RATE_LIMITED",
        "message": "Generation is temporarily rate limited. Try again later.",
        "details": None,
        "retryable": True,
        "retry_after_seconds": 13.0,
    }
    assert "secret" not in response.text and "raw provider" not in response.text
    assert len(calls) == 1
    status = client.get(f"/internal/v1/job/{payload['job_id']}/status").json()
    assert status["error_code"] == "RATE_LIMITED"
    assert status["retryable"] is True
    assert status["retry_after_seconds"] == 13
