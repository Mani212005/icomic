"""FastAPI entry point for Icomic Python AI service."""

from fastapi import FastAPI

from .gen.models import HealthResponse


def create_app() -> FastAPI:
    app = FastAPI(
        title="Icomic AI Service Internal API",
        version="1.0.0",
        description="Internal HTTP contract between Go API and Python AI service.",
    )

    @app.get("/healthz", response_model=HealthResponse)
    def health_check():
        return HealthResponse(status="ok")

    return app


app = create_app()
