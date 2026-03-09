import logging
import os

import uvicorn

from .app import app


def configure_logging() -> None:
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
    )


def main() -> None:
    configure_logging()
    host = os.environ.get("MANAGER_HOST", "127.0.0.1")
    uvicorn.run(app, host=host, port=9000, log_level="error")


if __name__ == "__main__":
    main()
