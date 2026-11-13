"""Earlier generation and export URLs survive regeneration through the public API."""

import httpx
from conftest import artifact_url


def test_regeneration_preserves_previous_artifacts(api_client, ai_service):
    def post(path, **kwargs):
        response = api_client.post(path, **kwargs)
        assert response.is_success, response.text
        return response.json()

    def image_bytes(url):
        # Fetch the same stored object through the task's ephemeral AI service.
        key = url.split("/storage/fake/", 1)[-1].split("/media/", 1)[-1]
        response = httpx.get(f"{ai_service['base_url']}/storage/fake/{key}")
        assert response.status_code == 200, response.text
        return response.content

    project = post(
        "/api/v1/projects",
        json={
            "title": "Immutable artifacts",
            "original_script": "Mira finds a clock in the harbor.",
            "chapter_count": 1,
            "art_style": "classic-comic",
        },
    )["project"]["id"]
    post(f"/api/v1/projects/{project}/analyze")
    characters = api_client.get(f"/api/v1/projects/{project}/characters").json()["characters"]
    for character in characters:
        path = f"/api/v1/characters/{character['id']}"
        first = post(f"{path}/reference-image")
        first_url = artifact_url(api_client, first)
        original = image_bytes(first_url)
        second = post(f"{path}/reference-image", json={"prompt": "A new reference pose"})
        assert second["job"]["id"] != first["job"]["id"]
        assert artifact_url(api_client, second) != first_url
        assert image_bytes(first_url) == original
        post(f"{path}/approve")

    chapter = post(f"/api/v1/projects/{project}/chapters")["chapters"][0]["id"]
    post(f"/api/v1/chapters/{chapter}/approve")
    page = post(f"/api/v1/chapters/{chapter}/panels")["pages"][0]["id"]
    for stage in ("draft", "line-art"):
        path = f"/api/v1/pages/{page}/{stage}"
        first = post(path)
        first_url = artifact_url(api_client, first)
        original = image_bytes(first_url)
        if stage == "line-art":
            # Current approval freshness uses page UpdatedAt, including inking.
            post(f"/api/v1/pages/{page}/draft/approve")
        second = post(path)
        assert second["job"]["id"] != first["job"]["id"]
        assert artifact_url(api_client, second) != first_url
        assert image_bytes(first_url) == original
        post(f"{path}/approve")

    first = post(f"/api/v1/projects/{project}/export")["export"]
    original = api_client.get(f"/api/v1/exports/{first['id']}/download").content
    changed = api_client.patch(f"/api/v1/projects/{project}", json={"title": "A new cover title"})
    assert changed.status_code == 200, changed.text
    second = post(f"/api/v1/projects/{project}/export")["export"]
    assert second["file_url"] != first["file_url"]
    assert api_client.get(f"/api/v1/exports/{second['id']}/download").content != original
    assert api_client.get(f"/api/v1/exports/{first['id']}/download").content == original
