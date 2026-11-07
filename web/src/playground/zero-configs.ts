import { useI18n } from '@/i18n'
import type { Model } from '@/types'
import { Capability, hasCode, isOmniModel } from './catalog'

// ZeroStarter 是对话空态的示例提示词：可带参考图片与开启的工具
export interface ZeroStarter {
  title: string
  prompt: string
  images?: string[]
  search?: boolean
  codeExecution?: boolean
}

// ZeroExample 是对话空态右侧的示例卡片，点击与示例提示词相同
interface ZeroExample extends ZeroStarter {
  preview: string
  video?: string
  detection?: { label: string; inset?: string }
}

// ZeroConfig 是官网按模型系列配置的对话空态
interface ZeroConfig {
  id: string
  kind: 'chat' | 'images' | 'robotics' | 'video'
  family: string
  title: string
  subtitle: string
  docs?: string
  builtBy?: string
  starters: ZeroStarter[]
  examples: ZeroExample[]
  matches: (model: Model) => boolean
}

const { t } = useI18n()

// zeroImage 是官网空态示例资源的地址
const zeroImage = (name: string) => `https://www.gstatic.com/aistudio/zero-state/${name}`

// maxVideoSeconds 返回 Omni 模型目录中视频时长的上限，没有视频时长选项时为 0
function maxVideoSeconds(model: Model): number {
  const item = model.capability_options?.video_duration_range?.find((entry) =>
    entry.startsWith('max='),
  )
  return Number(item?.slice(4) ?? 0)
}

// zeroConfigs 返回当前界面语言的官网对话页空态配置，按官网顺序取第一个匹配的配置
function zeroConfigs(): ZeroConfig[] {
  return [
    {
      id: 'robotics',
      kind: 'robotics',
      family: 'Gemini Robotics-ER',
      title: t('pg.roboticsEmptyTitle'),
      subtitle: t('zero.robotics.subtitle'),
      starters: [
        {
          title: t('zero.robotics.starter1'),
          prompt: t('zero.robotics.starter1Prompt'),
          images: [zeroImage('robotics_er_1_5_parts.webp')],
        },
        {
          title: t('zero.robotics.starter2'),
          prompt: t('zero.robotics.starter2Prompt'),
          images: [zeroImage('robotics_er_1_5_workspace.webp')],
        },
        {
          title: t('zero.robotics.starter3'),
          prompt: t('zero.robotics.starter3Prompt'),
          images: [zeroImage('robotics_er_1_5_gauge_v2.webp')],
        },
      ],
      examples: [
        {
          title: t('zero.robotics.example1'),
          prompt: t('zero.robotics.example1Prompt'),
          preview: zeroImage('robotics_er_1_5_parts.webp'),
          images: [zeroImage('robotics_er_1_5_parts.webp')],
          detection: { label: t('zero.robotics.example1Detection') },
        },
        {
          title: t('zero.robotics.example2'),
          prompt: t('zero.robotics.example2Prompt'),
          preview: zeroImage('robotics_er_1_5_workspace.webp'),
          images: [zeroImage('robotics_er_1_5_workspace.webp')],
          detection: { label: t('zero.robotics.example2Detection'), inset: '34% 34% 42% 50%' },
        },
      ],
      matches: (model) => model.id.includes('robotics'),
    },
    {
      id: 'omni-1-2',
      kind: 'video',
      family: 'Gemini Omni 1.2 Flash',
      title: t('pg.videoEmptyTitle'),
      subtitle: t('zero.omni12.subtitle'),
      starters: [
        { title: t('zero.omni12.starter1'), prompt: t('zero.omni12.starter1Prompt') },
        { title: t('zero.omni12.starter2'), prompt: t('zero.omni12.starter2Prompt') },
        { title: t('zero.omni12.starter3'), prompt: t('zero.omni12.starter3Prompt') },
      ],
      examples: [
        {
          title: t('zero.omni12.example1'),
          prompt: t('zero.omni12.example1Prompt'),
          preview: zeroImage('omni_1_2_ramen_bar.webp'),
          video: zeroImage('omni_1_2_ramen_bar.mp4'),
          images: [
            zeroImage('omni_1_2_ramen_first_frame.webp'),
            zeroImage('omni_1_2_ramen_last_frame.webp'),
          ],
        },
        {
          title: t('zero.omni12.example2'),
          prompt: t('zero.omni12.example2Prompt'),
          preview: zeroImage('omni_1_2_conservatory_voices.webp'),
          video: zeroImage('omni_1_2_conservatory_voices.mp4'),
          images: [
            'location',
            'location_lineup',
            'pip_1',
            'pip_2',
            'pip_3',
            'cosmo_1',
            'cosmo_2',
            'cosmo_3',
            'arthur_1',
            'arthur_2',
            'arthur_3',
            'mateo_1',
            'mateo_2',
            'mateo_3',
          ].map((name) => zeroImage(`omni_1_2_conservatory_${name}.webp`)),
        },
        {
          title: t('zero.omni12.example3'),
          prompt: t('zero.omni12.example3Prompt'),
          preview: zeroImage('omni_1_2_bee_hive.webp'),
          video: zeroImage('omni_1_2_bee_hive.mp4'),
          images: [zeroImage('omni_1_2_bee_hive_style_frame.webp')],
        },
      ],
      matches: (model) => isOmniModel(model) && maxVideoSeconds(model) >= 15,
    },
    {
      id: 'omni',
      kind: 'video',
      family: 'Gemini Omni',
      title: t('pg.videoEmptyTitle'),
      subtitle: t('zero.omni.subtitle'),
      starters: [
        {
          title: t('zero.omni.starter1'),
          prompt: t('zero.omni.starter1Prompt'),
          images: [zeroImage('omni-surfer-frame.webp')],
        },
        {
          title: t('zero.omni.starter2'),
          prompt: t('zero.omni.starter2Prompt'),
          images: [zeroImage('omni-submersible-frame.webp')],
        },
        {
          title: t('zero.omni.starter3'),
          prompt: t('zero.omni.starter3Prompt'),
          images: [zeroImage('omni-fashion-frame.webp')],
        },
      ],
      examples: [
        {
          title: t('zero.omni.example1'),
          prompt: t('zero.omni.example1Prompt'),
          preview: zeroImage('omni_ink_diorama.webp'),
          video: zeroImage('omni_ink_diorama.mp4'),
        },
        {
          title: t('zero.omni.example2'),
          prompt: t('zero.omni.example2Prompt'),
          preview: zeroImage('veo_3_1_tokyo_rain.webp'),
          video: zeroImage('veo_3_1_tokyo_rain.mp4'),
        },
      ],
      matches: isOmniModel,
    },
    {
      id: 'nano-banana-2-1',
      kind: 'images',
      family: 'Nano Banana',
      title: t('pg.imageEmptyTitle'),
      subtitle: t('zero.nanoBanana21.subtitle'),
      starters: [
        {
          title: t('zero.nanoBanana21.starter1'),
          prompt: t('zero.nanoBanana21.starter1Prompt'),
          images: [zeroImage('nb21-outfit-ref.webp')],
        },
        {
          title: t('zero.nanoBanana21.starter2'),
          prompt: t('zero.nanoBanana21.starter2Prompt'),
        },
        {
          title: t('zero.nanoBanana21.starter3'),
          prompt: t('zero.nanoBanana21.starter3Prompt'),
        },
      ],
      examples: [
        {
          title: t('zero.nanoBanana21.example1'),
          prompt: t('zero.nanoBanana21.example1Prompt'),
          preview: zeroImage('nano_banana_2_1_magazine_cover.webp'),
        },
        {
          title: t('zero.nanoBanana21.example2'),
          prompt: t('zero.nanoBanana21.example2Prompt'),
          preview: zeroImage('nano_banana_2_1_camera_knolling.webp'),
        },
        {
          title: t('zero.nanoBanana21.example3'),
          prompt: t('zero.nanoBanana21.example3Prompt'),
          preview: zeroImage('nano_banana_2_1_swiss_poster.webp'),
        },
      ],
      matches: (model) => model.id.includes('nano-banana-2.1'),
    },
    {
      id: 'nano-banana-lite',
      kind: 'images',
      family: 'Nano Banana 2 Lite',
      title: t('pg.imageEmptyTitle'),
      subtitle: t('zero.nanoBananaLite.subtitle'),
      starters: [
        {
          title: t('zero.nanoBananaLite.starter1'),
          prompt: t('zero.nanoBananaLite.starter1Prompt'),
          images: [zeroImage('nbl-corgi-ref.webp')],
        },
        {
          title: t('zero.nanoBananaLite.starter2'),
          prompt: t('zero.nanoBananaLite.starter2Prompt'),
          images: [zeroImage('nbl-kettle-ref.webp')],
        },
        {
          title: t('zero.nanoBananaLite.starter3'),
          prompt: t('zero.nanoBananaLite.starter3Prompt'),
        },
      ],
      examples: [
        {
          title: t('zero.nanoBananaLite.example1'),
          prompt: t('zero.nanoBananaLite.example1Prompt'),
          preview: zeroImage('nbl-sticker-sheet.webp'),
        },
        {
          title: t('zero.nanoBananaLite.example2'),
          prompt: t('zero.nanoBananaLite.example2Prompt'),
          preview: zeroImage('nbl-sneaker-colorways.webp'),
        },
        {
          title: t('zero.nanoBananaLite.example3'),
          prompt: t('zero.nanoBananaLite.example3Prompt'),
          preview: zeroImage('nbl-corgi-amalfi.webp'),
          images: [zeroImage('nbl-corgi-ref.webp')],
        },
      ],
      matches: (model) =>
        model.id.includes('flash-lite-image') || model.id.includes('nano-banana-lite'),
    },
    {
      id: 'nano-banana-images',
      kind: 'images',
      family: 'Nano Banana',
      title: t('pg.imageEmptyTitle'),
      subtitle: t('zero.nanoBanana.subtitle'),
      starters: [
        {
          title: t('zero.nanoBanana.starter1'),
          prompt: t('zero.nanoBanana.starter1Prompt'),
          images: [zeroImage('fashion.webp')],
        },
        {
          title: t('zero.nanoBanana.starter2'),
          prompt: t('zero.nanoBanana.starter2Prompt'),
          images: [zeroImage('fern.webp')],
        },
        {
          title: t('zero.nanoBanana.starter3'),
          prompt: t('zero.nanoBanana.starter3Prompt'),
          images: [zeroImage('surf.webp')],
        },
      ],
      examples: [
        {
          title: t('zero.nanoBanana.example1'),
          prompt: t('zero.nanoBanana.example1Prompt'),
          preview: zeroImage('nano_banana_ramen_diorama.webp'),
        },
        {
          title: t('zero.nanoBanana.example2'),
          prompt: t('zero.nanoBanana.example2Prompt'),
          preview: zeroImage('nano_banana_pro_perfume_ad.webp'),
          images: [zeroImage('perfume_bottle_no_logo.png'), zeroImage('rainne_logo.png')],
        },
        {
          title: t('zero.nanoBanana.example3'),
          prompt: t('zero.nanoBanana.example3Prompt'),
          preview: zeroImage('imagen_4_origami_sloth.webp'),
        },
      ],
      matches: (model) =>
        !model.id.includes('nano-banana-2.1') &&
        !model.id.includes('flash-lite-image') &&
        !model.id.includes('nano-banana-lite') &&
        (hasCode(model, Capability.ImageRoute) ||
          model.id.includes('image') ||
          model.id.includes('nano-banana')),
    },
    {
      id: 'flash-lite',
      kind: 'chat',
      family: 'Gemini Flash-Lite',
      title: t('pg.startBuilding'),
      subtitle: t('zero.flashLite.subtitle'),
      starters: [
        {
          title: t('zero.flashLite.starter1'),
          prompt: t('zero.flashLite.starter1Prompt'),
        },
        {
          title: t('zero.flashLite.starter2'),
          prompt: t('zero.flashLite.starter2Prompt'),
        },
        {
          title: t('zero.flashLite.starter3'),
          prompt: t('zero.flashLite.starter3Prompt'),
        },
      ],
      examples: [
        {
          title: t('zero.flashLite.example1'),
          prompt: t('zero.flashLite.example1Prompt'),
          preview:
            'https://www.gstatic.com/aistudio/starter-apps/zero-state-assets/gridscape-16-9.webp',
        },
        {
          title: t('zero.flashLite.example2'),
          prompt: t('zero.flashLite.example2Prompt'),
          preview:
            'https://www.gstatic.com/aistudio/starter-apps/zero-state-assets/peekaword-3-2.webp',
        },
        {
          title: t('zero.flashLite.example3'),
          prompt: t('zero.flashLite.example3Prompt'),
          preview:
            'https://www.gstatic.com/aistudio/starter-apps/zero-state-assets/moodboard-3-2.webp',
        },
      ],
      builtBy: t('zero.builtBy'),
      matches: (model) =>
        model.id.startsWith('gemini-') &&
        model.id.includes('flash-lite') &&
        !model.id.includes('image'),
    },
    {
      id: 'flash-chat',
      kind: 'chat',
      family: 'Gemini Flash',
      title: t('pg.startBuilding'),
      subtitle: t('zero.flash.subtitle'),
      starters: [
        {
          title: t('zero.flash.starter1'),
          prompt: t('zero.flash.starter1Prompt'),
        },
        {
          title: t('zero.flash.starter2'),
          prompt: t('zero.flash.starter2Prompt'),
        },
        {
          title: t('zero.flash.starter3'),
          prompt: t('zero.flash.starter3Prompt'),
          search: true,
        },
      ],
      examples: [
        {
          title: t('zero.flash.example1'),
          prompt: t('zero.flash.example1Prompt'),
          preview:
            'https://www.gstatic.com/aistudio/starter-apps/zero-state-assets/tickr-16-9.webp',
        },
        {
          title: t('zero.flash.example2'),
          prompt: t('zero.flash.example2Prompt'),
          preview:
            'https://www.gstatic.com/aistudio-static/applets/interactive-annual-report/image.png',
        },
        {
          title: t('zero.flash.example3'),
          prompt: t('zero.flash.example3Prompt'),
          preview:
            'https://www.gstatic.com/aistudio/starter-apps/zero-state-assets/dinodraft-3-2.webp',
        },
      ],
      builtBy: t('zero.builtBy'),
      matches: (model) =>
        model.id.startsWith('gemini-') &&
        model.id.includes('flash') &&
        !model.id.includes('flash-lite') &&
        !model.id.includes('image'),
    },
    {
      id: 'gemini-3-pro',
      kind: 'chat',
      family: 'Gemini Pro',
      title: t('pg.startBuilding'),
      subtitle: t('zero.pro.subtitle'),
      starters: [
        {
          title: t('zero.pro.starter1'),
          prompt: t('zero.pro.starter1Prompt'),
        },
        {
          title: t('zero.pro.starter2'),
          prompt: t('zero.pro.starter2Prompt'),
          codeExecution: true,
        },
        {
          title: t('zero.pro.starter3'),
          prompt: t('zero.pro.starter3Prompt'),
          search: true,
        },
      ],
      examples: [
        {
          title: t('zero.pro.example1'),
          prompt: t('zero.pro.example1Prompt'),
          preview: 'https://www.gstatic.com/aistudio/starter-apps/thumbnails/voxel_toy_box.png',
        },
        {
          title: t('zero.pro.example2'),
          prompt: t('zero.pro.example2Prompt'),
          preview: 'https://www.gstatic.com/aistudio/starter-apps/thumbnails/shader_pilot.png',
        },
        {
          title: t('zero.pro.example3'),
          prompt: t('zero.pro.example3Prompt'),
          preview:
            'https://www.gstatic.com/aistudio/starter-apps/thumbnails/research_visualization.png',
        },
      ],
      matches: (model) =>
        model.id.startsWith('gemini-') && model.id.includes('pro') && !model.id.includes('image'),
    },
    {
      id: 'flagship-chat',
      kind: 'chat',
      family: 'Gemini',
      title: t('pg.chatEmptyTitle'),
      subtitle: t('zero.flagship.subtitle'),
      docs: 'https://aistudio.google.com/docs/latest-model',
      starters: [
        {
          title: t('zero.flagship.starter1'),
          prompt: t('zero.flagship.starter1Prompt'),
          search: true,
          codeExecution: true,
        },
        {
          title: t('zero.flagship.starter2'),
          prompt: t('zero.flagship.starter2Prompt'),
          search: true,
        },
        {
          title: t('zero.flagship.starter3'),
          prompt: t('zero.flagship.starter3Prompt'),
        },
      ],
      examples: [
        {
          title: t('zero.flagship.example1'),
          prompt: t('zero.flagship.example1Prompt'),
          preview: 'https://www.gstatic.com/aistudio-static/applets/wizard-chess-3d/image.png',
        },
        {
          title: t('zero.flagship.example2'),
          prompt: t('zero.flagship.example2Prompt'),
          preview:
            'https://www.gstatic.com/aistudio-static/applets/pocket-world-terrarium/image.png',
        },
        {
          title: t('zero.flagship.example3'),
          prompt: t('zero.flagship.example3Prompt'),
          preview: 'https://www.gstatic.com/aistudio-static/applets/desk-racer/image.png',
        },
      ],
      builtBy: t('zero.builtBy'),
      matches: (model) => model.id.startsWith('gemini-') && !model.id.includes('pro'),
    },
  ]
}

// zeroConfigFor 返回模型在当前界面语言下的空态配置，没有匹配的系列时为 undefined
export function zeroConfigFor(model: Model | undefined): ZeroConfig | undefined {
  return model === undefined ? undefined : zeroConfigs().find((config) => config.matches(model))
}
