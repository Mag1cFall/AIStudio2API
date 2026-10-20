import { ref } from 'vue'

// playingSample 是正在播放的试听地址
const playingSample = ref('')
// loadingSample 是正在加载的试听地址
const loadingSample = ref('')
let player: HTMLAudioElement | undefined

// stopSample 停止试听
function stopSample(): void {
  player?.pause()
  playingSample.value = ''
  loadingSample.value = ''
}

// toggleSample 播放试听地址，正在播放同一地址时停止
async function toggleSample(url: string): Promise<void> {
  if (playingSample.value === url) {
    stopSample()
    return
  }
  stopSample()
  if (!url) return
  player ??= new Audio()
  player.onended = stopSample
  player.onerror = stopSample
  player.src = url
  playingSample.value = url
  loadingSample.value = url
  try {
    await player.play()
  } catch {
    stopSample()
    return
  }
  if (loadingSample.value === url) loadingSample.value = ''
}

// useSamplePlayer 返回页面共用的音色与示例试听播放器
export function useSamplePlayer() {
  return { playingSample, loadingSample, toggleSample, stopSample }
}
