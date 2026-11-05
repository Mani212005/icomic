"""Build the deployment image and prove it includes sibling Go package files.

Run with Python 3 and Docker, without credentials or running services.
Only base image pulls and dependency installation need network access.
"""

from pathlib import Path
import shutil
import subprocess
import tempfile
import uuid


def main():
    backend = Path(__file__).resolve().parents[1] / "backend"
    identity = uuid.uuid4().hex
    image = f"icomic-backend-build-check:{identity}"
    container = f"icomic-backend-build-check-{identity}"
    marker = "ICOMIC_SERVER_PACKAGE_PROBE"
    with tempfile.TemporaryDirectory(prefix="icomic-backend-build-") as directory:
        context = Path(directory) / "backend"
        shutil.copytree(backend, context, ignore=shutil.ignore_patterns(".env*"))
        # An init in a new sibling file detects even a file-list build that
        # happens to name all of today's production files successfully.
        (context / "cmd/server/container_build_probe.go").write_text(
            'package main\nimport ("fmt"; "os")\n'
            f'func init() {{ if os.Getenv("{marker}") == "1" {{ '
            f'fmt.Println("{marker}"); os.Exit(0) }} }}\n',
            encoding="utf-8",
        )
        try:
            subprocess.run(
                ["docker", "build", "--file", str(context / "Dockerfile"),
                 "--tag", image, str(context)],
                check=True,
            )
            result = subprocess.run(
                ["docker", "run", "--rm", "--name", container,
                 "--network", "none", "--env",
                 f"{marker}=1", image],
                check=True, capture_output=True, text=True, timeout=15,
            )
            if result.stdout.strip() != marker:
                raise RuntimeError("backend image omitted a server package file")
            print("Backend deployment image includes sibling server package files.")
        finally:
            subprocess.run(
                ["docker", "container", "rm", "--force", container],
                check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            )
            subprocess.run(
                ["docker", "image", "rm", image],
                check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            )


if __name__ == "__main__":
    main()
