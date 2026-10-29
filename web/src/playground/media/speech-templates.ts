import { useI18n } from '@/i18n'
import type { IconName } from '../ui/icons'

// SpeechTemplate 是语音页的一个示例对话：说话人声音、分段风格与台词、示例音频
export interface SpeechTemplate {
  id: string
  name: string
  icon: IconName
  description: string
  conversational: boolean
  speakers: { voice: string }[]
  turns: { speaker: number; style: string; text: string }[]
  audio: string
}

const { t } = useI18n()

// speechTemplates 返回当前界面语言的官网语音页示例对话
export function speechTemplates(): SpeechTemplate[] {
  return [
    {
      id: 'tts-everyday-assistant',
      name: t('zero.ttsAssistant.name'),
      icon: 'supportAgent',
      description: t('zero.ttsAssistant.description'),
      conversational: false,
      speakers: [{ voice: 'Fola' }],
      turns: [
        {
          speaker: 0,
          style: t('zero.ttsAssistant.turn1Style'),
          text: t('zero.ttsAssistant.turn1Text'),
        },
        {
          speaker: 0,
          style: t('zero.ttsAssistant.turn2Style'),
          text: t('zero.ttsAssistant.turn2Text'),
        },
        {
          speaker: 0,
          style: t('zero.ttsAssistant.turn3Style'),
          text: t('zero.ttsAssistant.turn3Text'),
        },
      ],
      audio: 'https://www.gstatic.com/aistudio/tts/assistant.wav',
    },
    {
      id: 'tts-guarded-npc',
      name: t('zero.ttsNpc.name'),
      icon: 'sportsEsports',
      description: t('zero.ttsNpc.description'),
      conversational: false,
      speakers: [{ voice: 'Cruz' }, { voice: 'Zeno' }],
      turns: [
        { speaker: 0, style: t('zero.ttsNpc.turn1Style'), text: t('zero.ttsNpc.turn1Text') },
        { speaker: 1, style: t('zero.ttsNpc.turn2Style'), text: t('zero.ttsNpc.turn2Text') },
        { speaker: 0, style: t('zero.ttsNpc.turn3Style'), text: t('zero.ttsNpc.turn3Text') },
        { speaker: 1, style: t('zero.ttsNpc.turn4Style'), text: t('zero.ttsNpc.turn4Text') },
        { speaker: 0, style: t('zero.ttsNpc.turn5Style'), text: t('zero.ttsNpc.turn5Text') },
      ],
      audio: 'https://www.gstatic.com/aistudio/tts/the-guarded-npc.wav',
    },
    {
      id: 'tts-energetic-co-host',
      name: t('zero.ttsCoHost.name'),
      icon: 'podcasts',
      description: t('zero.ttsCoHost.description'),
      conversational: true,
      speakers: [{ voice: 'Veda' }, { voice: 'Rami' }],
      turns: [
        { speaker: 0, style: t('zero.ttsCoHost.turn1Style'), text: t('zero.ttsCoHost.turn1Text') },
        { speaker: 1, style: t('zero.ttsCoHost.turn2Style'), text: t('zero.ttsCoHost.turn2Text') },
        { speaker: 0, style: t('zero.ttsCoHost.turn3Style'), text: t('zero.ttsCoHost.turn3Text') },
        { speaker: 1, style: t('zero.ttsCoHost.turn4Style'), text: t('zero.ttsCoHost.turn4Text') },
        { speaker: 0, style: t('zero.ttsCoHost.turn5Style'), text: t('zero.ttsCoHost.turn5Text') },
      ],
      audio: 'https://www.gstatic.com/aistudio/tts/enthusiastic-co-host.wav',
    },
    {
      id: 'tts-master-storyteller',
      name: t('zero.ttsStoryteller.name'),
      icon: 'menuBook',
      description: t('zero.ttsStoryteller.description'),
      conversational: false,
      speakers: [{ voice: 'Riko' }],
      turns: [
        {
          speaker: 0,
          style: t('zero.ttsStoryteller.turn1Style'),
          text: t('zero.ttsStoryteller.turn1Text'),
        },
        {
          speaker: 0,
          style: t('zero.ttsStoryteller.turn2Style'),
          text: t('zero.ttsStoryteller.turn2Text'),
        },
        {
          speaker: 0,
          style: t('zero.ttsStoryteller.turn3Style'),
          text: t('zero.ttsStoryteller.turn3Text'),
        },
      ],
      audio: 'https://www.gstatic.com/aistudio/tts/the-master-storyteller.wav',
    },
    {
      id: 'tts-ad-voiceover',
      name: t('zero.ttsAd.name'),
      icon: 'adsClick',
      description: t('zero.ttsAd.description'),
      conversational: false,
      speakers: [{ voice: 'Tavi' }],
      turns: [
        { speaker: 0, style: t('zero.ttsAd.turn1Style'), text: t('zero.ttsAd.turn1Text') },
        { speaker: 0, style: t('zero.ttsAd.turn2Style'), text: t('zero.ttsAd.turn2Text') },
        { speaker: 0, style: t('zero.ttsAd.turn3Style'), text: t('zero.ttsAd.turn3Text') },
      ],
      audio: 'https://www.gstatic.com/aistudio/tts/ad-voiceover.wav',
    },
    {
      id: 'tts-training-guide',
      name: t('zero.ttsTrainer.name'),
      icon: 'developerGuide',
      description: t('zero.ttsTrainer.description'),
      conversational: false,
      speakers: [{ voice: 'Kira' }],
      turns: [
        {
          speaker: 0,
          style: t('zero.ttsTrainer.turn1Style'),
          text: t('zero.ttsTrainer.turn1Text'),
        },
        {
          speaker: 0,
          style: t('zero.ttsTrainer.turn2Style'),
          text: t('zero.ttsTrainer.turn2Text'),
        },
        {
          speaker: 0,
          style: t('zero.ttsTrainer.turn3Style'),
          text: t('zero.ttsTrainer.turn3Text'),
        },
      ],
      audio: 'https://www.gstatic.com/aistudio/tts/corporate-trainer.wav',
    },
    {
      id: 'tts-game-show-host',
      name: t('zero.ttsGameShow.name'),
      icon: 'stars',
      description: t('zero.ttsGameShow.description'),
      conversational: false,
      speakers: [{ voice: 'Mako' }],
      turns: [
        {
          speaker: 0,
          style: t('zero.ttsGameShow.turn1Style'),
          text: t('zero.ttsGameShow.turn1Text'),
        },
        {
          speaker: 0,
          style: t('zero.ttsGameShow.turn2Style'),
          text: t('zero.ttsGameShow.turn2Text'),
        },
        {
          speaker: 0,
          style: t('zero.ttsGameShow.turn3Style'),
          text: t('zero.ttsGameShow.turn3Text'),
        },
      ],
      audio: 'https://www.gstatic.com/aistudio/tts/trivia.wav',
    },
    {
      id: 'tts-patient-teacher',
      name: t('zero.ttsTeacher.name'),
      icon: 'voiceSelection',
      description: t('zero.ttsTeacher.description'),
      conversational: true,
      speakers: [{ voice: 'Lumi' }, { voice: 'Varo' }],
      turns: [
        {
          speaker: 0,
          style: t('zero.ttsTeacher.turn1Style'),
          text: t('zero.ttsTeacher.turn1Text'),
        },
        {
          speaker: 1,
          style: t('zero.ttsTeacher.turn2Style'),
          text: t('zero.ttsTeacher.turn2Text'),
        },
        {
          speaker: 0,
          style: t('zero.ttsTeacher.turn3Style'),
          text: t('zero.ttsTeacher.turn3Text'),
        },
        {
          speaker: 1,
          style: t('zero.ttsTeacher.turn4Style'),
          text: t('zero.ttsTeacher.turn4Text'),
        },
        {
          speaker: 0,
          style: t('zero.ttsTeacher.turn5Style'),
          text: t('zero.ttsTeacher.turn5Text'),
        },
      ],
      audio: 'https://www.gstatic.com/aistudio/tts/language-tutor.wav',
    },
  ]
}
