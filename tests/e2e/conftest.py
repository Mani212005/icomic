"""Pytest fixtures for Icomic end-to-end suite.

Starts the real Python AI service on fakes and the real Go API server as local processes
on ephemeral ports with clean teardown, zero live API calls, and zero external network.
"""

from __future__ import annotations

import os
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path
from typing import Generator

import httpx
import pytest

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
AI_DIR = REPO_ROOT / "ai"
BACKEND_DIR = REPO_ROOT / "backend"
E2E_DIR = Path(__file__).resolve().parent

if str(E2E_DIR) not in sys.path:
    sys.path.insert(0, str(E2E_DIR))
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))


def find_free_port() -> int:
    """Allocate an available ephemeral TCP port on localhost."""
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def wait_for_health(url: str, timeout: float = 20.0) -> None:
    """Poll an HTTP health endpoint until it returns 200 OK or times out."""
    deadline = time.time() + timeout
    last_err: Exception | None = None
    while time.time() < deadline:
        try:
            res = httpx.get(url, timeout=1.0)
            if res.status_code == 200:
                return
        except Exception as e:
            last_err = e
        time.sleep(0.1)
    raise TimeoutError(f"Service at {url} did not become ready within {timeout}s: {last_err}")


def terminate_process(proc: subprocess.Popen) -> None:
    """Gracefully terminate a subprocess and its process group, falling back to SIGKILL."""
    if proc.poll() is not None:
        return
    try:
        os.killpg(os.getpgid(proc.pid), signal.SIGTERM)
    except (ProcessLookupError, PermissionError):
        pass

    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
        except (ProcessLookupError, PermissionError):
            pass
        proc.wait()


@pytest.fixture(scope="session")
def repo_root() -> Path:
    return REPO_ROOT


@pytest.fixture(scope="session")
def temp_test_dir() -> Generator[Path, None, None]:
    with tempfile.TemporaryDirectory(prefix="icomic-e2e-") as d:
        yield Path(d)


@pytest.fixture(scope="session")
def compiled_go_server(temp_test_dir: Path) -> Path:
    """Compile the Go API server binary once for all e2e test runs."""
    binary_path = temp_test_dir / "icomic-server"
    cmd = ["go", "build", "-o", str(binary_path), "./cmd/server"]
    res = subprocess.run(cmd, cwd=str(BACKEND_DIR), capture_output=True, text=True)
    if res.returncode != 0:
        raise RuntimeError(f"Failed to compile Go server binary: {res.stderr}")
    return binary_path


@pytest.fixture(scope="session")
def ai_service(temp_test_dir: Path) -> Generator[dict[str, Any], None, None]:
    """Start the real Python FastAPI service on deterministic fake adapters and ephemeral port."""
    ai_port = find_free_port()
    env = os.environ.copy()
    env["ADAPTER_LANGUAGE"] = "fake"
    env["ADAPTER_VISION"] = "fake"
    env["ADAPTER_IMAGE"] = "fake"
    env["ADAPTER_STORAGE"] = "fake"
    env["ADAPTER_QUEUE"] = "fake"
    env["PYTHONPATH"] = "."

    cmd = [
        "uv",
        "run",
        "uvicorn",
        "app.main:app",
        "--host",
        "127.0.0.1",
        "--port",
        str(ai_port),
    ]

    log_file = (temp_test_dir / "uvicorn.log").open("w+")
    proc = subprocess.Popen(
        cmd,
        cwd=str(AI_DIR),
        env=env,
        preexec_fn=os.setsid,
        stdout=log_file,
        stderr=subprocess.STDOUT,
    )

    base_url = f"http://127.0.0.1:{ai_port}"
    try:
        wait_for_health(f"{base_url}/healthz", timeout=20.0)
    except Exception as exc:
        terminate_process(proc)
        log_file.seek(0)
        logs = log_file.read()
        log_file.close()
        raise RuntimeError(
            f"Python AI service failed to start at {base_url}:\nLOGS: {logs}"
        ) from exc

    # Start transparent port 8000 forwarder if port 8000 is free.
    # Note: FakeStorageAdapter in ai/app/adapters/storage/fake.py currently hardcodes base_url='http://localhost:8000/storage/fake'.
    # This forwarder allows Go's download proxy to resolve the storage URL while running on ephemeral test ports.
    proxy_sock = None
    stop_event = threading.Event()
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s.bind(("127.0.0.1", 8000))
        s.listen(10)
        s.settimeout(0.5)
        proxy_sock = s

        def _forward_loop():
            while not stop_event.is_set():
                try:
                    client, _ = proxy_sock.accept()
                except socket.timeout:
                    continue
                except OSError:
                    break
                target = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                try:
                    target.connect(("127.0.0.1", ai_port))
                except Exception:
                    client.close()
                    continue

                def _pipe(src, dst):
                    try:
                        while True:
                            buf = src.recv(8192)
                            if not buf:
                                break
                            dst.sendall(buf)
                    except Exception:
                        pass
                    finally:
                        src.close()
                        dst.close()

                threading.Thread(target=_pipe, args=(client, target), daemon=True).start()
                threading.Thread(target=_pipe, args=(target, client), daemon=True).start()

        forwarder_thread = threading.Thread(target=_forward_loop, daemon=True)
        forwarder_thread.start()
    except Exception:
        # Port 8000 is occupied or restricted; fallback proxy behavior documented in KNOWN_ISSUES.md
        proxy_sock = None

    yield {
        "port": ai_port,
        "base_url": base_url,
        "proc": proc,
        "port_8000_forwarded": proxy_sock is not None,
    }

    if proxy_sock is not None:
        stop_event.set()
        try:
            proxy_sock.close()
        except Exception:
            pass

    terminate_process(proc)
    log_file.close()


@pytest.fixture(scope="session")
def go_api_server(
    request: pytest.FixtureRequest,
    compiled_go_server: Path,
    ai_service: dict[str, Any],
    temp_test_dir: Path,
) -> Generator[dict[str, Any], None, None]:
    """Start the real Go API server connected to the running Python AI service."""
    go_port = find_free_port()
    media_dir = temp_test_dir / "media"
    media_dir.mkdir(parents=True, exist_ok=True)

    env = os.environ.copy()
    env["PORT"] = str(go_port)
    env["PYTHON_AI_URL"] = ai_service["base_url"]
    env["DEV_AUTH_ENABLED"] = "true"
    env["MEDIA_DIR"] = str(media_dir)
    env["KILL_SWITCH"] = "false"
    env["LETTERING_OVERLAY_ENABLED"] = "true" if getattr(request, "param", False) else "false"

    # Wire real PostgreSQL if TEST_DATABASE_URL is provided, otherwise default to in-memory fake
    if "TEST_DATABASE_URL" in os.environ and os.environ["TEST_DATABASE_URL"]:
        env["DATABASE_URL"] = os.environ["TEST_DATABASE_URL"]
    else:
        env.pop("DATABASE_URL", None)

    go_log_file = (temp_test_dir / "go-server.log").open("w+")
    proc = subprocess.Popen(
        [str(compiled_go_server)],
        cwd=str(BACKEND_DIR),
        env=env,
        preexec_fn=os.setsid,
        stdout=go_log_file,
        stderr=subprocess.STDOUT,
    )

    base_url = f"http://127.0.0.1:{go_port}"
    try:
        wait_for_health(f"{base_url}/healthz", timeout=15.0)
    except Exception as exc:
        terminate_process(proc)
        go_log_file.seek(0)
        logs = go_log_file.read()
        go_log_file.close()
        raise RuntimeError(
            f"Go API server failed to start at {base_url}:\nLOGS: {logs}"
        ) from exc

    yield {
        "port": go_port,
        "base_url": base_url,
        "proc": proc,
        "media_dir": media_dir,
    }

    terminate_process(proc)
    go_log_file.close()


@pytest.fixture
def api_client(go_api_server: dict[str, Any]) -> Generator[httpx.Client, None, None]:
    """HTTP client authenticated with the supported dev identity (dev@icomic.local)."""
    headers = {
        "X-User-ID": "00000000-0000-0000-0000-000000000001",
        "Content-Type": "application/json",
    }
    with httpx.Client(base_url=go_api_server["base_url"], headers=headers, timeout=10.0) as client:
        yield client


@pytest.fixture
def kill_switch_server(
    compiled_go_server: Path,
    ai_service: dict[str, Any],
    temp_test_dir: Path,
) -> Generator[httpx.Client, None, None]:
    """Start an isolated Go API server instance with KILL_SWITCH=true active."""
    ks_port = find_free_port()
    media_dir = temp_test_dir / "ks_media"
    media_dir.mkdir(parents=True, exist_ok=True)

    env = os.environ.copy()
    env["PORT"] = str(ks_port)
    env["PYTHON_AI_URL"] = ai_service["base_url"]
    env["DEV_AUTH_ENABLED"] = "true"
    env["MEDIA_DIR"] = str(media_dir)
    env["KILL_SWITCH"] = "true"
    env.pop("DATABASE_URL", None)

    ks_log = (temp_test_dir / "kill-switch-server.log").open("w+")
    proc = subprocess.Popen(
        [str(compiled_go_server)],
        cwd=str(BACKEND_DIR),
        env=env,
        preexec_fn=os.setsid,
        stdout=ks_log,
        stderr=subprocess.STDOUT,
    )

    base_url = f"http://127.0.0.1:{ks_port}"
    try:
        wait_for_health(f"{base_url}/healthz", timeout=15.0)
    except Exception as exc:
        terminate_process(proc)
        ks_log.seek(0)
        logs = ks_log.read()
        ks_log.close()
        raise RuntimeError(
            f"Kill switch Go API server failed to start at {base_url}:\nLOGS: {logs}"
        ) from exc

    headers = {
        "X-User-ID": "00000000-0000-0000-0000-000000000001",
        "Content-Type": "application/json",
    }
    with httpx.Client(base_url=base_url, headers=headers, timeout=10.0) as client:
        yield client

    terminate_process(proc)
    ks_log.close()


@pytest.fixture
def quota_server(
    compiled_go_server: Path,
    ai_service: dict[str, Any],
    temp_test_dir: Path,
) -> Generator[httpx.Client, None, None]:
    """Start an isolated Go API server instance for quota exhaustion testing."""
    q_port = find_free_port()
    media_dir = temp_test_dir / "quota_media"
    media_dir.mkdir(parents=True, exist_ok=True)

    env = os.environ.copy()
    env["PORT"] = str(q_port)
    env["PYTHON_AI_URL"] = ai_service["base_url"]
    env["DEV_AUTH_ENABLED"] = "true"
    env["MEDIA_DIR"] = str(media_dir)
    env["KILL_SWITCH"] = "false"
    env.pop("DATABASE_URL", None)

    q_log = (temp_test_dir / "quota-server.log").open("w+")
    proc = subprocess.Popen(
        [str(compiled_go_server)],
        cwd=str(BACKEND_DIR),
        env=env,
        preexec_fn=os.setsid,
        stdout=q_log,
        stderr=subprocess.STDOUT,
    )

    base_url = f"http://127.0.0.1:{q_port}"
    try:
        wait_for_health(f"{base_url}/healthz", timeout=15.0)
    except Exception as exc:
        terminate_process(proc)
        q_log.seek(0)
        logs = q_log.read()
        q_log.close()
        raise RuntimeError(
            f"Quota Go API server failed to start at {base_url}:\nLOGS: {logs}"
        ) from exc

    headers = {
        "X-User-ID": "00000000-0000-0000-0000-000000000001",
        "Content-Type": "application/json",
    }
    with httpx.Client(base_url=base_url, headers=headers, timeout=10.0) as client:
        yield client

    terminate_process(proc)
    q_log.close()
