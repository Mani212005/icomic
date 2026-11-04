"""Generation results retain exact original bytes across successful and failed retries."""

from io import BytesIO
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from PIL import Image

from app.adapters.clock.fake import FakeClockAdapter
from app.adapters.factory import AdapterSet
from app.adapters.image.fake import FakeImageAdapter
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.queue.fake import FakeQueueAdapter
from app.adapters.storage.fake import FakeStorageAdapter
from app.adapters.vision.fake import FakeVisionAdapter
from app.main import create_app


def png(colour):
    buffer = BytesIO()
    Image.new("RGB", (12, 12), colour).save(buffer, format="PNG")
    return buffer.getvalue()


@pytest.mark.parametrize("operation", ["character-reference", "draft-sketch", "line-art", "pdf"])
def test_saved_attempt_url_keeps_original_bytes(operation):
    storage = FakeStorageAdapter()
    image = FakeImageAdapter()
    queue = FakeQueueAdapter()
    app = create_app(
        AdapterSet(
            language=FakeLanguageAdapter(),
            vision=FakeVisionAdapter(),
            image=image,
            storage=storage,
            queue=queue,
            clock=FakeClockAdapter(),
        )
    )
    source = storage.put("source.png", png("black"), "image/png")
    payload = {"job_id": str(uuid4()), "project_id": str(uuid4()), "hashed_user_id": "test-user"}
    if operation == "character-reference":
        payload.update(
            character_id=str(uuid4()), character_name="Mira", prompt="Mira", style="comic"
        )
    elif operation == "pdf":
        payload.update(
            title="First cover",
            page_size="A4",
            include_ai_disclosure=True,
            pages=[{"page_number": 1, "image_url": source}],
        )
    else:
        payload.update(page_id=str(uuid4()), page_number=1, panels=[], style="comic")
        if operation == "line-art":
            payload["draft_image_url"] = source
    endpoint = f"/internal/v1/{'assemble-pdf' if operation == 'pdf' else 'generate-' + operation}"
    url_field = "pdf_url" if operation == "pdf" else "image_url"
    with TestClient(app) as client:
        image._png_bytes = png("red")
        first = client.post(endpoint, json=payload)
        assert first.status_code == 200, first.text
        first_url = first.json()[url_field]
        original = storage.get(first_url)
        second_payload = dict(payload, job_id=str(uuid4()))
        if operation == "pdf":
            second_payload["title"] = "Second cover"
        image._png_bytes = png("blue")
        second = client.post(endpoint, json=second_payload)
        assert second.status_code == 200, second.text
        second_url = second.json()[url_field]
        assert storage.get(first_url) == original
        assert second_url != first_url
        assert storage.get(second_url) != original
        # The queue's saved attempt response is the durable attempt-to-asset link.
        assert queue.get_job("generation", payload["job_id"])["response"] == first.json()
        assert client.post(endpoint, json=payload).json() == first.json()

        # Simulate storage writing bytes then raising: a failed regeneration must
        # leave the earlier successful artifact and its saved response intact.
        put = storage.put

        def fail_after_write(key, data, content_type):
            put(key, data, content_type)
            raise OSError("storage acknowledgement lost")

        storage.put = fail_after_write
        image._png_bytes = png("green")
        failed_payload = dict(payload, job_id=str(uuid4()))
        if operation == "pdf":
            failed_payload["title"] = "Failed cover"
        failed = client.post(endpoint, json=failed_payload)
        assert failed.status_code == 500
        assert storage.get(first_url) == original
        assert queue.get_job("generation", payload["job_id"])["response"] == first.json()
