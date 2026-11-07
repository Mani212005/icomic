"""Vision subject boxes for lettering: parsing, bounds, cost guard, fallback and gating."""

import base64
import io
import json
from uuid import uuid4

import httpx
import pytest
from fastapi.testclient import TestClient
from PIL import Image

from app.adapters.vision.fake import FakeVisionAdapter
from app.adapters.vision.meta import MetaVisionAdapter
from app.adapters.vision.port import SubjectBox, VisionPort
from app.adapters.vision.subjects import (
    MAX_VISION_SIDE,
    downscale_for_vision,
    parse_subjects,
    subjects_prompt,
)
from app.costs import GENERATION_POLICIES
from app.main import create_app
from app.provider_output import ProviderOutputError
from app.services.lettering.subjects import (
    detect_page_subjects,
    subject_detection_enabled,
    to_art_subjects,
)
from tests.image_fixtures import image_bytes

MOCK_BASE_URL = "http://mock.meta.internal/v1"
REPLY = {
    "subjects": [
        {"kind": "face", "character": "nia", "confidence": 0.93, "box": [0.1, 0.2, 0.2, 0.15]},
        {"kind": "bird", "character": None, "confidence": 0.8, "box": [0.6, 0.1, 0.2, 0.1]},
        {"kind": "hand", "character": "Nia", "confidence": 0.7, "box": [0.3, 0.5, 0.1, 0.08]},
    ]
}


def _chat(content: str) -> dict:
    return {
        "id": "chatcmpl-subjects",
        "model": "muse-spark-1.3",
        "choices": [
            {
                "index": 0,
                "message": {"role": "assistant", "content": content},
                "finish_reason": "stop",
            }
        ],
        "usage": {"prompt_tokens": 900, "completion_tokens": 120, "total_tokens": 1020},
    }


def _big_page(size=(2000, 3000)) -> bytes:
    out = io.BytesIO()
    Image.new("RGB", size, (30, 60, 90)).save(out, format="PNG")
    return out.getvalue()


# -- parsing ---------------------------------------------------------------------------


def test_valid_reply_becomes_normalized_boxes_with_known_character_names():
    boxes = parse_subjects(json.dumps(REPLY), ["Nia", "Finn"])

    assert [b.kind for b in boxes] == ["face", "bird", "hand"]
    assert boxes[0].character == "Nia"  # Matched case-insensitively to the supplied name.
    assert boxes[1].character is None
    assert boxes[0].x == pytest.approx(0.1) and boxes[0].confidence == pytest.approx(0.93)


def test_fenced_json_is_accepted_and_invalid_boxes_are_dropped_not_repaired():
    reply = {
        "subjects": [
            *REPLY["subjects"][:1],
            {"kind": "dragon", "confidence": 0.9, "box": [0.1, 0.1, 0.1, 0.1]},
            {"kind": "face", "confidence": 2.0, "box": [0.1, 0.1, 0.1, 0.1]},
            {"kind": "face", "confidence": 0.9, "box": [0.1, 0.1, 0.0, 0.1]},
            {"kind": "face", "confidence": 0.9, "box": [0.0, 0.0, 1.0, 1.0]},
            {"kind": "face", "confidence": 0.9, "box": [0.1, 0.1, float("nan"), 0.1]},
            {"kind": "face", "confidence": 0.9, "box": [0.1, 0.1]},
            "face",
        ]
    }
    # json.dumps writes NaN, which strict parsers reject: that box must not survive either way.
    fenced = "```json\n" + json.dumps(reply) + "\n```"

    boxes = parse_subjects(fenced, ["Nia"])

    assert len(boxes) == 1 and boxes[0].kind == "face"


@pytest.mark.parametrize(
    "content",
    [
        "I see a lighthouse keeper and a bird.",
        "{",
        "[]",
        json.dumps({"subjects": []}),
        json.dumps({"subjects": "none"}),
        json.dumps({"subjects": [{"kind": "face", "confidence": 0.9, "box": [0, 0, 1, 1]}]}),
        json.dumps({"type": "object", "properties": {"subjects": {}}}),
    ],
)
def test_junk_is_rejected_whole(content):
    with pytest.raises(ProviderOutputError):
        parse_subjects(content, ["Nia"])


def test_box_count_is_bounded():
    many = {"subjects": [REPLY["subjects"][1]] * 500}
    assert len(parse_subjects(json.dumps(many), [])) == 40


# -- downscaling and the single bounded call -------------------------------------------


def test_page_is_downscaled_to_at_most_1024_px_on_the_long_edge():
    small = Image.open(io.BytesIO(downscale_for_vision(_big_page())))
    assert max(small.size) == MAX_VISION_SIDE and small.size[0] < small.size[1]

    untouched = Image.open(io.BytesIO(downscale_for_vision(_big_page((300, 400)))))
    assert untouched.size == (300, 400)


def test_meta_adapter_sends_one_downscaled_capped_request_and_parses_the_boxes():
    captured: list[dict] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured.append(json.loads(request.content))
        return httpx.Response(200, json=_chat(json.dumps(REPLY)))

    adapter = MetaVisionAdapter(
        api_key="mock-key-123", base_url=MOCK_BASE_URL, transport=httpx.MockTransport(handler)
    )
    assert isinstance(adapter, VisionPort)

    boxes = adapter.detect_subjects("muse-spark-1.3", _big_page(), ["Nia"])

    assert len(boxes) == 3 and adapter.last_usage is not None
    assert len(captured) == 1  # One call per page, no retries.
    payload = captured[0]
    policy = GENERATION_POLICIES["subjects"]
    assert payload["max_tokens"] == policy.answer_tokens + policy.reasoning_reserve
    url = payload["messages"][0]["content"][1]["image_url"]["url"]
    sent = Image.open(io.BytesIO(base64.b64decode(url.split(",", 1)[1])))
    assert max(sent.size) <= MAX_VISION_SIDE
    assert "Nia" in payload["messages"][0]["content"][0]["text"]


def test_meta_adapter_does_not_retry_junk_or_http_errors():
    calls = []

    def junk(request: httpx.Request) -> httpx.Response:
        calls.append(1)
        return httpx.Response(200, json=_chat("no boxes here"))

    adapter = MetaVisionAdapter(
        api_key="mock-key-123", base_url=MOCK_BASE_URL, transport=httpx.MockTransport(junk)
    )
    with pytest.raises(ProviderOutputError):
        adapter.detect_subjects("muse-spark-1.3", _big_page((400, 600)), [])
    assert len(calls) == 1


def test_the_prompt_names_characters_and_asks_for_normalized_boxes():
    prompt = subjects_prompt(["Nia", "Finn"])
    assert "Nia, Finn" in prompt and "normalized" in prompt and '"box"' in prompt


# -- fake adapter, fallback and gating -------------------------------------------------


def test_fake_adapter_is_deterministic_and_can_replay_a_fixture():
    fake = FakeVisionAdapter()
    assert fake.detect_subjects("", b"", ["Nia"]) == fake.detect_subjects("", b"", ["Nia"])
    fixture = [SubjectBox("bird", 0.1, 0.1, 0.1, 0.1, 1.0)]
    assert FakeVisionAdapter(subject_fixture=fixture).detect_subjects("", b"", []) == fixture


def test_detection_returns_wire_boxes_and_the_vision_source():
    subjects, source = detect_page_subjects(FakeVisionAdapter(), b"", ["Nia"])

    assert source == "vision" and subjects[0].character == "Nia"
    art = to_art_subjects(subjects)
    assert art[0].kind == "face" and art[0].box.width == pytest.approx(0.18)


class _Failing(FakeVisionAdapter):
    def __init__(self, error):
        super().__init__()
        self.error = error

    def detect_subjects(self, model, image_bytes, characters, **kwargs):
        self.subject_calls += 1
        raise self.error


@pytest.mark.parametrize(
    "error",
    [
        ProviderOutputError("subjects", "schema_mismatch"),
        RuntimeError("provider cost guard refused the call"),
        httpx.ConnectError("down"),
        ValueError("junk"),
    ],
)
def test_any_failure_falls_back_to_heuristics_without_retrying(error):
    adapter = _Failing(error)

    subjects, source = detect_page_subjects(adapter, b"", ["Nia"])

    assert (subjects, source) == ([], "fallback") and adapter.subject_calls == 1


def test_an_empty_answer_is_treated_as_a_fallback_not_as_an_empty_page():
    empty = FakeVisionAdapter(subject_fixture=[])

    assert detect_page_subjects(empty, b"", [])[1] == "fallback"


def test_detection_follows_the_overlay_switch_unless_overridden():
    assert subject_detection_enabled({"LETTERING_OVERLAY_ENABLED": "true"})
    assert not subject_detection_enabled({})
    assert not subject_detection_enabled(
        {"LETTERING_OVERLAY_ENABLED": "true", "LETTERING_SUBJECT_DETECTION_ENABLED": "false"}
    )
    assert subject_detection_enabled({"LETTERING_SUBJECT_DETECTION_ENABLED": "true"})


# -- the painted page endpoint ----------------------------------------------------------


def _painted_payload(app, with_document: bool):
    storage = app.state.storage_adapter
    char_url = storage.put("nia.png", image_bytes("nia-identity"), "image/png")
    payload = {
        "job_id": str(uuid4()),
        "hashed_user_id": "test-user-hash",
        "project_id": str(uuid4()),
        "page_id": str(uuid4()),
        "page_number": 1,
        "style": "classic-comic",
        "variant": "direct-from-brief",
        "panels": [
            {
                "panel_number": 1,
                "scene_description": "Nia holds the brass bird",
                "character_actions": "Raises the bird",
                "camera_angle": "Medium",
                "background": "Stormy sea",
                "dialogue": "Nia: Follow the light!",
                "narration": "Dawn.",
                "image_prompt": "Warm lantern glow",
            }
        ],
        "character_identities": [
            {
                "character_id": "nia",
                "name": "Nia",
                "description": "Keeper",
                "visual_attributes": {},
                "reference_image_url": char_url,
            }
        ],
    }
    if with_document:
        payload["lettering_document"] = {
            "version": "lettering-v1",
            "page_id": payload["page_id"],
            "art_mode": "text_free",
            "page_aspect": 2 / 3,
            "panels": [
                {
                    "panel_id": "panel-1",
                    "order": 0,
                    "rect": {"x": 0.03, "y": 0.02, "width": 0.94, "height": 0.96},
                    "reserved_zones": [{"x": 0.04, "y": 0.04, "width": 0.92, "height": 0.35}],
                    "utterances": [
                        {
                            "utterance_id": "panel-1-dialogue-0",
                            "order": 0,
                            "kind": "speech",
                            "text": "Follow the light!",
                            "speaker": "Nia",
                        }
                    ],
                }
            ],
        }
    return payload


def test_overlay_page_gets_one_vision_call_and_returns_boxes(monkeypatch):
    monkeypatch.setenv("LETTERING_OVERLAY_ENABLED", "true")
    app = create_app()
    vision = app.state.vision_adapter
    with TestClient(app) as client:
        response = client.post(
            "/internal/v1/generate-painted-page", json=_painted_payload(app, True)
        )

    assert response.status_code == 200, response.text
    body = response.json()
    assert body["lettering_subjects_source"] == "vision"
    assert body["lettering_subjects"][0]["character"] == "Nia"
    assert vision.subject_calls == 1


def test_baked_page_or_disabled_switch_makes_no_vision_call(monkeypatch):
    monkeypatch.setenv("LETTERING_OVERLAY_ENABLED", "false")
    app = create_app()
    with TestClient(app) as client:
        response = client.post(
            "/internal/v1/generate-painted-page", json=_painted_payload(app, True)
        )

    assert response.status_code == 200, response.text
    assert response.json().get("lettering_subjects_source") is None
    assert app.state.vision_adapter.subject_calls == 0
