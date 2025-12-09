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

    def to_dict(self) -> dict:
        return {
            "id": self.id,
            "profile": self.profile_name,
            "port": self.port,
            "camoufox_port": self.camoufox_port,
            "status": self.status,
            "request_count": self.request_count,
            "rate_limited_models": {
                model: recovery_time 
                for model, recovery_time in self.rate_limited_models.items()
            }
        }
