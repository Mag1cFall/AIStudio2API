import os
import shutil

from fastapi import APIRouter, Body, HTTPException

try:
    from ...config.settings import ACTIVE_AUTH_DIR, SAVED_AUTH_DIR
except ImportError:
    from config.settings import ACTIVE_AUTH_DIR, SAVED_AUTH_DIR


router = APIRouter(prefix="/api/auth", tags=["Auth"])


@router.get("/files")
async def list_auth_files():
    active_file = None
    if os.path.exists(ACTIVE_AUTH_DIR):
        files = [
            file_name
            for file_name in os.listdir(ACTIVE_AUTH_DIR)
            if file_name.endswith(".json")
        ]
        if files:
            active_file = files[0]

    saved_files = []
    if os.path.exists(SAVED_AUTH_DIR):
        saved_files = [
            file_name
            for file_name in os.listdir(SAVED_AUTH_DIR)
            if file_name.endswith(".json")
        ]

    return {"active": active_file, "saved": saved_files}


@router.post("/activate")
async def activate_auth(filename: str = Body(..., embed=True)):
    os.makedirs(ACTIVE_AUTH_DIR, exist_ok=True)

    for file_name in os.listdir(ACTIVE_AUTH_DIR):
        if file_name.endswith(".json"):
            os.remove(os.path.join(ACTIVE_AUTH_DIR, file_name))

    source = os.path.join(SAVED_AUTH_DIR, filename)
    if not os.path.exists(source):
        source = os.path.join(ACTIVE_AUTH_DIR, filename)
        if not os.path.exists(source):
            raise HTTPException(status_code=404, detail="文件不存在")

    shutil.copy2(source, os.path.join(ACTIVE_AUTH_DIR, filename))
    return {"success": True}


@router.post("/deactivate")
async def deactivate_auth():
    if os.path.exists(ACTIVE_AUTH_DIR):
        for file_name in os.listdir(ACTIVE_AUTH_DIR):
            if not file_name.endswith(".json"):
                continue
            try:
                os.remove(os.path.join(ACTIVE_AUTH_DIR, file_name))
            except Exception as exc:
                raise HTTPException(
                    status_code=500, detail=f"删除文件失败: {exc}"
                ) from exc
    return {"success": True}


@router.post("/rename")
async def rename_auth(
    old_name: str = Body(..., embed=True), new_name: str = Body(..., embed=True)
):
    if not new_name.endswith(".json"):
        new_name = f"{new_name}.json"

    old_path = os.path.join(SAVED_AUTH_DIR, old_name)
    new_path = os.path.join(SAVED_AUTH_DIR, new_name)

    if not os.path.exists(old_path):
        raise HTTPException(status_code=404, detail="文件不存在")
    if os.path.exists(new_path):
        raise HTTPException(status_code=400, detail="目标文件名已存在")

    try:
        os.rename(old_path, new_path)
        return {"success": True, "new_name": new_name}
    except Exception as exc:
        raise HTTPException(status_code=500, detail=str(exc)) from exc
