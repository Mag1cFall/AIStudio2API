// bytesToBase64 把字节编码为 Base64
export function bytesToBase64(bytes: Uint8Array): string {
  let binary = ''
  for (let index = 0; index < bytes.length; index += 0x8000)
    binary += String.fromCharCode(...bytes.subarray(index, index + 0x8000))
  return btoa(binary)
}

// base64Bytes 把 Base64 文本解码为字节
export function base64Bytes(data: string): Uint8Array<ArrayBuffer> {
  return Uint8Array.from(atob(data), (char) => char.charCodeAt(0))
}

// pcmToWav 把 16 位小端 PCM 片段封装为 WAV
export function pcmToWav(
  chunks: readonly Uint8Array<ArrayBuffer>[],
  rate: number,
  channels = 1,
): Blob {
  const size = chunks.reduce((total, chunk) => total + chunk.length, 0)
  const header = new DataView(new ArrayBuffer(44))
  const text = (offset: number, value: string): void => {
    for (let index = 0; index < value.length; index++)
      header.setUint8(offset + index, value.charCodeAt(index))
  }
  text(0, 'RIFF')
  header.setUint32(4, 36 + size, true)
  text(8, 'WAVEfmt ')
  header.setUint32(16, 16, true)
  header.setUint16(20, 1, true)
  header.setUint16(22, channels, true)
  header.setUint32(24, rate, true)
  header.setUint32(28, rate * channels * 2, true)
  header.setUint16(32, channels * 2, true)
  header.setUint16(34, 16, true)
  text(36, 'data')
  header.setUint32(40, size, true)
  return new Blob([header, ...chunks], { type: 'audio/wav' })
}
