import type { IconName } from '../ui/icons'

// SpeechTemplate 是语音页的一个示例对话：说话人声音与风格、分段台词与示例音频
export interface SpeechTemplate {
  id: string
  name: string
  icon: IconName
  description: string
  multiSpeaker: boolean
  conversational: boolean
  speakers: { voice: string; style: string }[]
  turns: { speaker: number; style: string; text: string }[]
  audio: string
}

// speechTemplates 是官网语音页的示例对话
export const speechTemplates: SpeechTemplate[] = [
  {
    id: 'tts-everyday-assistant',
    name: 'The Everyday Assistant',
    icon: 'supportAgent',
    description: 'A helpful and professional personal assistant.',
    multiSpeaker: false,
    conversational: false,
    speakers: [
      {
        voice: 'Fola',
        style:
          'Bright, sunny, welcoming morning greeting with a subtle vocal smile and crisp pacing',
      },
    ],
    turns: [
      {
        speaker: 0,
        style:
          'Bright, sunny, welcoming morning greeting with a subtle vocal smile and crisp pacing',
        text: "Good morning! It's 68 degrees and sunny outside. Traffic on the bridge is already backing up a bit, <short pause> so I'd suggest heading out ten minutes early for your nine o'clock meeting.",
      },
      {
        speaker: 0,
        style:
          'Warm, deeply empathetic, soft and reassuring tone de-escalating a frustrating billing issue',
        text: '<sigh> Now, regarding your support ticket from yesterday, <short pause> I completely understand how frustrating that duplicate billing error was.',
      },
      {
        speaker: 0,
        style: 'Confident, efficient, and encouraging wrap-up with clear articulation',
        text: "<chuckles> The good news is I've already submitted and confirmed a full refund on your behalf, <short pause> so you'll see those funds back in your account within three business days.",
      },
    ],
    audio: 'https://www.gstatic.com/aistudio/tts/assistant.wav',
  },
  {
    id: 'tts-guarded-npc',
    name: 'The Guarded NPC',
    icon: 'sportsEsports',
    description: 'Creates multi-character dialogue in a fantasy setting.',
    multiSpeaker: true,
    conversational: false,
    speakers: [
      {
        voice: 'Cruz',
        style: 'Commanding, stern, weathered voice calling out across a cold stone courtyard',
      },
      {
        voice: 'Zeno',
        style: 'Firm, urgent, and fearless determination with brisk, clipped pacing',
      },
    ],
    turns: [
      {
        speaker: 0,
        style: 'Commanding, stern, weathered voice calling out across a cold stone courtyard',
        text: '<throat-clearing> Halt, traveler! Not another step. The northern pass is sealed by order of the High Council.',
      },
      {
        speaker: 1,
        style: 'Firm, urgent, and fearless determination with brisk, clipped pacing',
        text: "<exhales> I carry a sealed dispatch for the Elder. Step aside, <short pause> or I'll find my own way through.",
      },
      {
        speaker: 0,
        style: 'Somber, guarded, and hesitant, dropping volume as painful memories surface',
        text: '<sighs> Put your blade away. No one passes... <long pause> because the Elder is no longer receiving visitors.',
      },
      {
        speaker: 1,
        style: 'Sharp, suspicious, and tense, leaning in to demand an explanation',
        text: "What are you hiding, old man? The valley is burning, <short pause> we don't have time for riddles!",
      },
      {
        speaker: 0,
        style: 'Hushed, terrified close-mic whisper building suddenly into a desperate warning',
        text: '<whispers> Keep your voice down! The shadow reached the citadel before dawn. <gasp> Listen—do you hear that? You need to run. Now!',
      },
    ],
    audio: 'https://www.gstatic.com/aistudio/tts/the-guarded-npc.wav',
  },
  {
    id: 'tts-energetic-co-host',
    name: 'The Energetic Co-Host',
    icon: 'podcasts',
    description: 'Podcast style conversation.',
    multiSpeaker: true,
    conversational: true,
    speakers: [
      {
        voice: 'Veda',
        style: 'Upbeat, enthusiastic podcast intro with a bright vocal smile and brisk tempo',
      },
      { voice: 'Rami', style: 'Animated, warm agreement with eager conversational momentum' },
    ],
    turns: [
      {
        speaker: 0,
        style: 'Upbeat, enthusiastic podcast intro with a bright vocal smile and brisk tempo',
        text: 'Welcome back to the show, everyone! Today we are diving straight into the wild intersection of generative AI |Mm-hmm.| and human creativity.',
      },
      {
        speaker: 1,
        style: 'Animated, warm agreement with eager conversational momentum',
        text: "<chuckles> Oh, I've been waiting all week to talk about this. The pace right now is honestly dizzying.",
      },
      {
        speaker: 0,
        style: 'Playful, curious, and leaning into the mic as if sharing a secret',
        text: 'Right? I mean, did you catch that live voice demo they dropped on Tuesday afternoon?',
      },
      {
        speaker: 1,
        style: 'Amazed, expressive, and slightly breathless with genuine excitement',
        text: '<gasp> Stop, I replayed it three times! <chuckles> When the model cleared its throat and whispered mid-sentence, |Right!| my jaw actually hit the desk.',
      },
      {
        speaker: 0,
        style: 'Warm, thoughtful, and engaging transition into the main topic',
        text: '<sigh> It really blurs the line between a tool and a performance partner, <short pause> so grab your coffee, folks, because we have a lot to unpack today.',
      },
    ],
    audio: 'https://www.gstatic.com/aistudio/tts/enthusiastic-co-host.wav',
  },
  {
    id: 'tts-master-storyteller',
    name: 'The Master Storyteller',
    icon: 'menuBook',
    description: 'Crafts storytelling narration.',
    multiSpeaker: false,
    conversational: false,
    speakers: [
      {
        voice: 'Riko',
        style: 'Rich, velvety, unhurried audiobook narration setting an atmospheric evening scene',
      },
    ],
    turns: [
      {
        speaker: 0,
        style: 'Rich, velvety, unhurried audiobook narration setting an atmospheric evening scene',
        text: 'Blackwood House stood silent at the edge of the windswept moor, its shuttered windows dark save for a single amber lantern flickering on the third floor.',
      },
      {
        speaker: 0,
        style: 'Hushed, gravelly, urgent character voice speaking a tense warning from the shadows',
        text: '"Put that lantern out, Elias," a weathered voice rasped from the stone archway, <short pause> "if the hounds catch our scent on the wind, we won\'t make the river by dawn."',
      },
      {
        speaker: 0,
        style:
          'Tense, propelling narrative cadence building momentum as the character springs into action',
        text: 'Without a second thought, Elias snuffed the flame between his fingers, vaulted the mossy courtyard wall, and sprinted into the swirling mist.',
      },
    ],
    audio: 'https://www.gstatic.com/aistudio/tts/the-master-storyteller.wav',
  },
  {
    id: 'tts-ad-voiceover',
    name: 'The Ad Voiceover',
    icon: 'adsClick',
    description: 'A smooth, premium commercial voice.',
    multiSpeaker: false,
    conversational: false,
    speakers: [
      {
        voice: 'Tavi',
        style:
          'Peppy, bright, and playful commercial voiceover hooking the listener with curious suspense',
      },
    ],
    turns: [
      {
        speaker: 0,
        style:
          'Peppy, bright, and playful commercial voiceover hooking the listener with curious suspense',
        text: "Wait—don't touch that dial for three seconds! Have you ever wondered why your afternoon energy crashes right when your biggest ideas finally show up?",
      },
      {
        speaker: 0,
        style: 'Fast, upbeat, and electric anticipation building suspense toward the big reveal',
        text: "Something brighter, fizzier, and ridiculously refreshing has been brewing behind the scenes, <short pause> and it's about to flip your daily routine upside down.",
      },
      {
        speaker: 0,
        style: 'High-energy, smiling, punchy commercial reveal with a crisp celebratory sign-off',
        text: '<chuckles> Meet Sunburst Sparkling Cold Brew—zero crash, double the spark, <short pause> and waiting in the chill aisle right now. Crack open the good stuff!',
      },
    ],
    audio: 'https://www.gstatic.com/aistudio/tts/ad-voiceover.wav',
  },
  {
    id: 'tts-training-guide',
    name: 'The Training Guide',
    icon: 'developerGuide',
    description: 'A clear and authoritative corporate trainer.',
    multiSpeaker: false,
    conversational: false,
    speakers: [
      {
        voice: 'Kira',
        style:
          'Warm, friendly, and inviting how-to host welcoming listeners to a practical home guide',
      },
    ],
    turns: [
      {
        speaker: 0,
        style:
          'Warm, friendly, and inviting how-to host welcoming listeners to a practical home guide',
        text: 'Welcome to our three-step guide to brewing café-quality pour-over coffee right in your own kitchen. First, rinse your paper filter with hot water to warm up the glass carafe.',
      },
      {
        speaker: 0,
        style: 'Encouraging, conversational hands-on coaching pointing out a helpful visual cue',
        text: "Next, add two tablespoons of medium-ground coffee and pour just enough water in a slow spiral to wet the grounds. <short pause> See those little golden bubbles rising? <chuckles> That's called the bloom.",
      },
      {
        speaker: 0,
        style: 'Steady, reassuring, and upbeat wrap-up completing the recipe',
        text: 'Give it thirty seconds to breathe, <short pause> then pour the rest of your water slowly into the center—and your morning cup is ready to enjoy.',
      },
    ],
    audio: 'https://www.gstatic.com/aistudio/tts/corporate-trainer.wav',
  },
  {
    id: 'tts-game-show-host',
    name: 'The Game Show Host',
    icon: 'stars',
    description: 'A vibrant and theatrical host.',
    multiSpeaker: false,
    conversational: false,
    speakers: [
      {
        voice: 'Mako',
        style: 'High-energy, booming, theatrical game show host hyping up the studio',
      },
    ],
    turns: [
      {
        speaker: 0,
        style: 'High-energy, booming, theatrical game show host hyping up the studio',
        text: '<chuckles> Welcome back to the lightning round on Brain Trust! Both teams are tied at four hundred, and everything comes down to Ancient History!',
      },
      {
        speaker: 0,
        style:
          'Brisk, rhythmic, suspenseful cadence building anticipation on each multiple-choice option',
        text: 'For five hundred points and the championship trophy: Which ancient wonder stood guard over the harbor of Alexandria? Was it A: The Hanging Gardens, <short pause> B: The Colossus of Rhodes, <short pause> or C: The Pharos Lighthouse?',
      },
      {
        speaker: 0,
        style: 'Intense dramatic countdown shifting into jubilant, celebratory triumph',
        text: 'Lock in those buzzers right now! <long pause> <gasp> You gambled everything on C... <short pause> and you are taking home the grand prize!',
      },
    ],
    audio: 'https://www.gstatic.com/aistudio/tts/trivia.wav',
  },
  {
    id: 'tts-patient-teacher',
    name: 'The Patient Teacher',
    icon: 'voiceSelection',
    description: 'A patient and encouraging language teacher.',
    multiSpeaker: true,
    conversational: true,
    speakers: [
      {
        voice: 'Lumi',
        style:
          'Warm, patient, and encouraging language teacher modeling clear Spanish vowel pronunciation',
      },
      {
        voice: 'Varo',
        style: 'Careful, slightly hesitant student sounding out each Spanish syllable thoughtfully',
      },
    ],
    turns: [
      {
        speaker: 0,
        style:
          'Warm, patient, and encouraging language teacher modeling clear Spanish vowel pronunciation',
        text: '<chuckles> You\'re doing so well today! Let\'s try this next phrase together, nice and slow: "El clima de hoy es absolutamente hermoso."',
      },
      {
        speaker: 1,
        style: 'Careful, slightly hesitant student sounding out each Spanish syllable thoughtfully',
        text: '<exhales> Okay, let me try... <short pause> El clima de hoy es... <short pause> absolutamente hermoso.',
      },
      {
        speaker: 0,
        style: 'Delighted, affirming, and gentle coaching with seamless bilingual switching',
        text: 'Spot on! Your cadence on "absolutamente" was super clean. Now let\'s order a coffee: "Me gustaría pedir un café cortado, por favor."',
      },
      {
        speaker: 1,
        style: 'Growing confidence and smoother flow, finishing with a relieved chuckle',
        text: 'Me gustaría pedir, <short pause> un café cortado, por favor. <chuckles> Hey, that one actually felt natural!',
      },
      {
        speaker: 0,
        style: 'Warm, proud, and uplifting wrap-up',
        text: "It sounded completely natural! You're ready for Madrid.",
      },
    ],
    audio: 'https://www.gstatic.com/aistudio/tts/language-tutor.wav',
  },
]
