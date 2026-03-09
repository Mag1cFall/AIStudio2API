import asyncio
import importlib
import json
import sys
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock

import pytest
from fastapi.testclient import TestClient

PROJECT_ROOT = Path(__file__).resolve().parents[1]
SOURCE_ROOT = PROJECT_ROOT / "src"
if str(SOURCE_ROOT) not in sys.path:
    sys.path.insert(0, str(SOURCE_ROOT))

service_module = importlib.import_module("manager.service")
ServiceManager = service_module.ServiceManager
Worker = importlib.import_module("worker.models").Worker
WorkerPool = importlib.import_module("worker.pool").WorkerPool
create_manager_app = importlib.import_module("manager.app").create_app


@pytest.fixture
def anyio_backend():
    return "asyncio"


class DummyStdout:
    def readline(self):
        return b""


class DummyProcess:
    def __init__(self, pid=1000, poll_result=None):
        self.pid = pid
        self._poll_result = poll_result
        self.stdout = DummyStdout()

    def poll(self):
        return self._poll_result

    def terminate(self):
        return None

    def wait(self, timeout=None):
        return 0

    def kill(self):
        return None


class DummyThread:
    def __init__(self, target=None, args=None, daemon=None):
        self.target = target
        self.args = args or ()
        self.daemon = daemon

    def start(self):
        return None


@pytest.mark.anyio
async def test_start_worker_mode_uses_staggered_startup(monkeypatch):
    manager = ServiceManager()
    fake_pool = SimpleNamespace()
    fake_pool.workers = {
        "w1": Worker("w1", "a.json", "a.json", 3001, 9222),
        "w2": Worker("w2", "b.json", "b.json", 3002, 9223),
    }
    fake_pool.init_from_config = Mock()
    fake_pool.configure_runtime = Mock()
    fake_pool.register_status_listener = Mock()

    started = []

    def fake_start_worker(worker_id):
        worker = fake_pool.workers[worker_id]
        worker.process = DummyProcess(pid=4100 + len(started))
        worker.status = "running"
        started.append(worker_id)
        return True, f"started {worker_id}"

    fake_pool.start_worker = Mock(side_effect=fake_start_worker)

    sleep_mock = AsyncMock()
    popen_calls = []

    def fake_popen(*args, **kwargs):
        popen_calls.append(args[0])
        return DummyProcess(pid=5100 + len(popen_calls))

    monkeypatch.setattr(service_module, "WORKER_POOL_AVAILABLE", True)
    monkeypatch.setattr(service_module, "worker_pool", fake_pool)
    monkeypatch.setattr(service_module.asyncio, "sleep", sleep_mock)
    monkeypatch.setattr(service_module.threading, "Thread", DummyThread)
    monkeypatch.setattr(service_module.subprocess, "Popen", fake_popen)

    success, _ = await manager._start_worker_mode(
        {"fastapi_port": 2048, "stream_port_enabled": False},
        "--headless",
        {},
        0,
    )

    assert success is True
    assert started == ["w1", "w2"]
    assert sleep_mock.await_count == 1
    sleep_mock.assert_awaited_with(3)
    assert any("gateway.py" in str(part) for part in popen_calls[0])


@pytest.mark.anyio
async def test_health_check_marks_worker_crashed_after_threshold(monkeypatch):
    pool = WorkerPool()
    events = []
    pool.register_status_listener(events.append)
    worker = Worker("w1", "a.json", "a.json", 3001, 9222)
    worker.status = "running"
    worker.process = DummyProcess()
    pool.workers[worker.id] = worker

    probe_mock = AsyncMock(return_value=(False, "health failed"))
    monkeypatch.setattr(pool, "_probe_worker_health", probe_mock)

    await pool.health_check(max_failures=3, auto_restart=False)
    await pool.health_check(max_failures=3, auto_restart=False)
    await pool.health_check(max_failures=3, auto_restart=False)

    assert worker.status == "crashed"
    assert worker.health_failures == 3
    assert worker.last_error == "health failed"
    assert events[-1]["status"] == "crashed"


@pytest.mark.anyio
async def test_stop_worker_waits_for_inflight_requests(monkeypatch):
    pool = WorkerPool()
    worker = Worker("w1", "a.json", "a.json", 3001, 9222)
    worker.status = "running"
    worker.process = DummyProcess()
    worker.active_requests = 1
    pool.workers[worker.id] = worker

    terminate_mock = Mock()
    monkeypatch.setattr(pool, "_terminate_worker_process", terminate_mock)

    stop_task = asyncio.create_task(
        pool.stop_worker("w1", graceful_timeout=0.2, poll_interval=0.01)
    )
    await asyncio.sleep(0.03)
    assert stop_task.done() is False

    worker.active_requests = 0
    success, message = await stop_task

    assert success is True
    assert "stopped" in message.lower()
    assert worker.status == "stopped"
    assert terminate_mock.call_count == 1


def test_worker_to_dict_exposes_rate_limited_display_status():
    worker = Worker("w1", "a.json", "a.json", 3001, 9222)
    worker.status = "running"
    worker.mark_model_limited("gemini-2.5-pro", hours=1)

    data = worker.to_dict()

    assert data["status"] == "running"
    assert data["display_status"] == "rate_limited"


def test_websocket_logs_sends_worker_snapshot(monkeypatch):
    service_module.manager.active_connections = []
    pool = service_module.worker_pool
    original_workers = pool.workers

    worker = Worker("w1", "a.json", "a.json", 3001, 9222)
    worker.status = "running"
    pool.workers = {worker.id: worker}

    monkeypatch.setattr(service_module.worker_pool, "init_from_config", lambda: None)

    app = create_manager_app()
    with TestClient(app) as client:
        with client.websocket_connect("/ws/logs") as websocket:
            status_message = json.loads(websocket.receive_text())
            snapshot_message = json.loads(websocket.receive_text())

    pool.workers = original_workers

    assert status_message["type"] == "status"
    assert snapshot_message["type"] == "worker_snapshot"
    assert snapshot_message["workers"][0]["id"] == "w1"
