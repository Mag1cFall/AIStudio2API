import { ref, watch } from 'vue'
import { readPreference, writePreference } from '@/preference'

// microphoneKey 是 User settings 中所选麦克风的本机存储键
const microphoneKey = 'studio_microphone'

// microphone 是 User settings 中所选麦克风的设备 ID，空字符串为系统默认设备
export const microphone = ref(
  readPreference(microphoneKey, '', (value) => typeof value === 'string'),
)

watch(microphone, (value) => writePreference(microphoneKey, value))

// SubmitKey 是 User settings 中运行提示词的快捷键：ctrl-enter 为 Ctrl+Enter，enter 为 Enter
export type SubmitKey = 'ctrl-enter' | 'enter'

// submitKeyKey 是运行快捷键的本机存储键
const submitKeyKey = 'studio_submit_key'

// submitKey 是 User settings 中所选的运行快捷键
export const submitKey = ref<SubmitKey>(
  readPreference(
    submitKeyKey,
    'ctrl-enter',
    (value) => value === 'ctrl-enter' || value === 'enter',
  ),
)

watch(submitKey, (value) => writePreference(submitKeyKey, value))

// audioInput 返回按所选麦克风打开音频输入的约束，设备不可用时浏览器改用其他设备
export function audioInput(): MediaTrackConstraints | true {
  return microphone.value === '' ? true : { deviceId: microphone.value }
}
