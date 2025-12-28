import asyncio
from typing import Optional, List, Tuple
from playwright.async_api import Page as AsyncPage, Locator


async def wait_for_any_selector(
    page: AsyncPage,
    selectors: List[str],
    timeout: int = 5000,
    state: str = 'visible'
) -> Tuple[Optional[Locator], Optional[str]]:
    async def check_one(selector: str) -> Tuple[bool, str]:
        try:
            locator = page.locator(selector)
            await locator.wait_for(state=state, timeout=timeout)
            return (True, selector)
        except:
            return (False, selector)
    
    tasks = [asyncio.create_task(check_one(sel)) for sel in selectors]
    
    async def cancel_tasks(pending_tasks):
        for p in pending_tasks:
            p.cancel()
        await asyncio.gather(*pending_tasks, return_exceptions=True)
    
    try:
        done, pending = await asyncio.wait(
            tasks,
            return_when=asyncio.FIRST_COMPLETED,
            timeout=timeout / 1000 + 1
        )
        
        for task in done:
            success, selector = task.result()
            if success:
                await cancel_tasks(pending)
                return (page.locator(selector), selector)
        
        await cancel_tasks(pending)
        return (None, None)
        
    except asyncio.TimeoutError:
        await cancel_tasks(tasks)
        return (None, None)



async def get_first_visible_locator(
    page: AsyncPage,
    selectors: List[str],
    timeout: int = 3000
) -> Tuple[Optional[Locator], Optional[str]]:
    for selector in selectors:
        try:
            locator = page.locator(selector)
            if await locator.count() > 0:
                first = locator.first
                if await first.is_visible():
                    return (first, selector)
        except:
            continue
    
    return await wait_for_any_selector(page, selectors, timeout)


async def click_first_available(
    page: AsyncPage,
    selectors: List[str],
    timeout: int = 5000
) -> Tuple[bool, Optional[str]]:
    locator, selector = await get_first_visible_locator(page, selectors, timeout)
    if locator:
        try:
            await locator.click(timeout=1000)
            return (True, selector)
        except:
            try:
                await locator.evaluate('el => el.click()')
                return (True, selector)
            except:
                pass
    return (False, None)
