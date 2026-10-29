import type { OrbState } from 'thinking-orbs/engine'
import { useI18n } from '@/i18n'
import type { VoiceDetail } from '@/types'

// SpeechVoice 是官网语音页 Voice Design 的一个示例声音：标题、描述、试听音频与卡片动画
interface SpeechVoice {
  id: string
  title: string
  description: string
  sample: string
  orb: OrbState
}

const { t } = useI18n()

// voiceTraits 返回目录音色的官网描述与音高
export function voiceTraits(voice: VoiceDetail): string[] {
  return [
    ...(voice.descriptions ?? []),
    ...(voice.pitch ? [t('pg.pitchValue').replace('{pitch}', voice.pitch)] : []),
  ]
}

// speechVoices 返回当前界面语言的官网语音页“Explore thousands of voices”示例声音
export function speechVoices(): SpeechVoice[] {
  return [
    {
      id: 'voice-grizzled-detective',
      title: t('zero.voiceDetective.title'),
      description: t('zero.voiceDetective.description'),
      sample: 'https://www.gstatic.com/aistudio/voices/samples/the-grizzled-detective.m4a',
      orb: 'shaping',
    },
    {
      id: 'voice-meditation-guide',
      title: t('zero.voiceMeditation.title'),
      description: t('zero.voiceMeditation.description'),
      sample: 'https://www.gstatic.com/aistudio/voices/samples/the-meditation-guide.m4a',
      orb: 'breathing',
    },
    {
      id: 'voice-hype-announcer',
      title: t('zero.voiceAnnouncer.title'),
      description: t('zero.voiceAnnouncer.description'),
      sample: 'https://www.gstatic.com/aistudio/voices/samples/the-hype-announcer.m4a',
      orb: 'working',
    },
    {
      id: 'voice-regal-monarch',
      title: t('zero.voiceMonarch.title'),
      description: t('zero.voiceMonarch.description'),
      sample: 'https://www.gstatic.com/aistudio/voices/samples/the-regal-monarch.m4a',
      orb: 'composing',
    },
    {
      id: 'voice-late-night-dj',
      title: t('zero.voiceDj.title'),
      description: t('zero.voiceDj.description'),
      sample: 'https://www.gstatic.com/aistudio/voices/samples/the-late-night-dj.m4a',
      orb: 'weaving',
    },
    {
      id: 'voice-cheeky-sidekick',
      title: t('zero.voiceSidekick.title'),
      description: t('zero.voiceSidekick.description'),
      sample: 'https://www.gstatic.com/aistudio/voices/samples/the-cheeky-sidekick.m4a',
      orb: 'solving',
    },
    {
      id: 'voice-mad-scientist',
      title: t('zero.voiceScientist.title'),
      description: t('zero.voiceScientist.description'),
      sample: 'https://www.gstatic.com/aistudio/voices/samples/the-mad-scientist.m4a',
      orb: 'searching',
    },
  ]
}
