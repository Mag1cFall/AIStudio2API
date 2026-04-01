import base64
from typing import Optional, Tuple


def decode_data_image_url(src: str) -> Optional[Tuple[str, bytes]]:
    if not src.startswith("data:image/") or "," not in src:
        return None

    header, base64_data = src.split(",", 1)
    mime_type = header.replace("data:", "").replace(";base64", "")
    return mime_type, base64.b64decode(base64_data)


async def extract_image_from_locator(locator) -> Optional[Tuple[str, bytes, str]]:
    src = await locator.evaluate(
        "img => img.currentSrc || img.getAttribute('src') || img.src || ''"
    )
    if not src:
        return None

    decoded = decode_data_image_url(src)
    if decoded:
        mime_type, image_bytes = decoded
        return src, image_bytes, mime_type

    extracted = await locator.evaluate(
        """
        async img => {
            const src = img.currentSrc || img.getAttribute('src') || img.src || '';
            if (!src) {
                return null;
            }

            try {
                const response = await fetch(src, { credentials: 'include' });
                if (!response.ok) {
                    return { src, error: `HTTP ${response.status}` };
                }

                const blob = await response.blob();
                const buffer = await blob.arrayBuffer();
                const bytes = new Uint8Array(buffer);
                const chunkSize = 0x8000;
                let binary = '';

                for (let i = 0; i < bytes.length; i += chunkSize) {
                    binary += String.fromCharCode(...bytes.subarray(i, i + chunkSize));
                }

                return {
                    src,
                    mimeType: blob.type || 'image/png',
                    data: btoa(binary),
                };
            } catch (error) {
                return { src, error: String(error) };
            }
        }
        """
    )

    if not extracted or extracted.get("error") or not extracted.get("data"):
        return None

    return (
        extracted.get("src", src),
        base64.b64decode(extracted["data"]),
        extracted.get("mimeType") or "image/png",
    )
