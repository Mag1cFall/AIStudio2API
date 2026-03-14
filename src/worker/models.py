from dataclasses import dataclass, field
from typing import Dict, Optional
import subprocess
import time


@dataclass
class Worker:
    id: str
    profile_name: str
    profile_path: str
    port: int
    camoufox_port: int
    process: Optional[subprocess.Popen] = None
    status: str = "stopped"
    rate_limited_models: Dict[str, float] = field(default_factory=dict)
    request_count: int = 0
    active_requests: int = 0
    health_failures: int = 0
    last_health_check: Optional[float] = None
    last_error: Optional[str] = None
    restart_count: int = 0

    def is_model_limited(self, model_id: str) -> bool:
        if model_id not in self.rate_limited_models:
            return False
        if time.time() >= self.rate_limited_models[model_id]:
            del self.rate_limited_models[model_id]
            return False
        return True

    def mark_model_limited(self, model_id: str, hours: float = 6.0):
        self.rate_limited_models[model_id] = time.time() + hours * 3600

    def clear_rate_limits(self):
        self.rate_limited_models.clear()

    def display_status(self) -> str:
        if self.status == "running" and self.rate_limited_models:
            return "rate_limited"
        return self.status

    def to_dict(self) -> dict:
        return {
            "id": self.id,
            "profile": self.profile_name,
            "port": self.port,
            "camoufox_port": self.camoufox_port,
            "status": self.status,
            "display_status": self.display_status(),
            "request_count": self.request_count,
            "active_requests": self.active_requests,
            "health_failures": self.health_failures,
            "last_health_check": self.last_health_check,
            "last_error": self.last_error,
            "restart_count": self.restart_count,
            "rate_limited_models": {
                model: recovery_time
                for model, recovery_time in self.rate_limited_models.items()
            },
        }
