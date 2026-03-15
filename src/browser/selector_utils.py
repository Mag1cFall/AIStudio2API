import asyncio
from typing import Optional, List, Tuple
from playwright.async_api import Page as AsyncPage, Locator


async def wait_for_any_selector(
    page: AsyncPage,
    selectors: List[str],
    timeout: int = 5000,
    state: str = 'visible'
) -> Tuple[Optional[Locator], Optional[str]]:
    combined = ", ".join(selectors)
    try:
        await page.locator(combined).first.wait_for(state=state, timeout=timeout)
    except Exception:
        return (None, None)

    for selector in selectors:
        try:
            loc = page.locator(selector)
            if await loc.count() > 0 and await loc.first.is_visible():
                return (loc, selector)
        except Exception:
            continue

    return (page.locator(combined), combined)



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
