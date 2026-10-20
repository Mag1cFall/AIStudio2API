import type { Model, ThinkingLevel } from '@/types'
import { hasCode } from './catalog'

// ZeroStarter 是对话空态的示例提示词：可带参考图片、思考档位与开启的工具
export interface ZeroStarter {
  title: string
  prompt: string
  images?: string[]
  thinkingLevel?: ThinkingLevel
  search?: boolean
  codeExecution?: boolean
}

// ZeroExample 是对话空态右侧的示例卡片，点击与示例提示词相同
export interface ZeroExample extends ZeroStarter {
  preview: string
  detection?: { label: string; inset?: string }
}

// ZeroConfig 是官网按模型系列配置的对话空态
export interface ZeroConfig {
  id: string
  kind: 'chat' | 'images' | 'robotics'
  family: string
  title: string
  subtitle: string
  docs?: string
  starters: ZeroStarter[]
  examples: ZeroExample[]
  matches: (model: Model) => boolean
}

// zeroImage 是官网空态示例资源的地址
const zeroImage = (name: string) => `https://www.gstatic.com/aistudio/zero-state/${name}`

// zeroConfigs 是官网对话页空态配置，按官网顺序取第一个匹配的配置
export const zeroConfigs: ZeroConfig[] = [
  {
    id: 'robotics',
    kind: 'robotics',
    family: 'Gemini Robotics-ER',
    title: 'Build with {model}',
    subtitle:
      'Embodied reasoning for the physical world: detect objects, read analog instruments, and plan collision-free manipulation trajectories.',
    starters: [
      {
        title: 'Plan the next move',
        prompt:
          'Using this claw-machine image, describe a conceptual sequence for positioning the claw over a toy. State any uncertainty about depth and occlusion.',
        images: [zeroImage('robotics_er_1_5_parts.webp')],
      },
      {
        title: 'Point to all fruits on the tabletop',
        prompt:
          'Point to all fruits in this tabletop scene and return their normalized 2D coordinates [y, x] from 0 to 1000 along with recommended gripper approach angles.',
        images: [zeroImage('robotics_er_1_5_workspace.webp')],
      },
      {
        title: 'Read an analog pressure gauge with geometry',
        prompt:
          'Read the red indicator of the gauge in bar. Detect the hub, needle tip, and nearby labeled ticks, then calculate the exact reading with math rather than guessing.',
        images: [zeroImage('robotics_er_1_5_gauge_v2.webp')],
      },
    ],
    examples: [
      {
        title: 'Find a toy to pick up',
        prompt:
          'Examine this claw-machine image. Locate a visible toy and return its bounding box as normalized coordinates from 0 to 1000. Describe its position relative to the claw.',
        preview: zeroImage('robotics_er_1_5_parts.webp'),
        images: [zeroImage('robotics_er_1_5_parts.webp')],
        detection: { label: 'toy [640, 455]' },
      },
      {
        title: 'Localize tabletop objects & grasp order',
        prompt:
          'Point to all fruits and tableware in this workspace. Return normalized 2D coordinates [y, x] from 0 to 1000 for each grasp center and plan a collision-free pick-and-place sequence.',
        preview: zeroImage('robotics_er_1_5_workspace.webp'),
        images: [zeroImage('robotics_er_1_5_workspace.webp')],
        detection: { label: 'fruit [380, 540]', inset: '34% 34% 42% 50%' },
      },
    ],
    matches: (model) => model.id.startsWith('gemini-robotics') || model.id.includes('robotics'),
  },
  {
    id: 'nano-banana-2-1',
    kind: 'images',
    family: 'Nano Banana',
    title: 'Generate images with {model}',
    subtitle:
      'Our latest native image generation and editing model with crisp typography, multi-image composition, and tactile realism.',
    starters: [
      {
        title: 'Pin an editorial fashion collage on corkboard',
        prompt:
          'Create a fashion product collage on a brown corkboard based on this outfit. Each product and the central model image should be presented as individual photos with distinct white borders, pinned organically with small descriptive hand-written labels in 16:9.',
        images: [zeroImage('nb21-outfit-ref.webp')],
        thinkingLevel: 'MEDIUM',
      },
      {
        title: 'Design a minimalist botanical bird wallpaper',
        prompt:
          'A beautiful wallpaper featuring a vibrant yellow Bananaquit bird perched on a green banana plant leaf beside a bunch of ripe bananas, with a warm top-to-bottom pastel gradient background and minimalist composition.',
        thinkingLevel: 'HIGH',
      },
      {
        title: 'Draft an architectural plan & section blueprint',
        prompt:
          'Create an orthographic architectural presentation sheet describing a cliffside Nordic glass-and-cedar pavilion in plan, elevation, and cross-section cutaways with crisp dimension callouts in 16:9.',
        thinkingLevel: 'HIGH',
      },
    ],
    examples: [
      {
        title: 'Glossy editorial magazine cover',
        prompt:
          'A photo of a glossy magazine cover, the minimal blue cover has the large bold words Nano Banana in a serif font filling the view. In front of the text is a portrait of a nonchalant person in a stylish autumnal dress with a repeating "2.1" pattern, with issue number 2.1, "Oct 2026" date, and a barcode in the corner on a designer store shelf.',
        preview: zeroImage('nano_banana_2_1_magazine_cover.webp'),
        thinkingLevel: 'HIGH',
      },
      {
        title: 'Disassembled brass rangefinder knolling',
        prompt:
          'Overhead knolling product photograph of an unbranded disassembled vintage brass-and-leather rangefinder camera arranged on a dark matte slate surface with multi-element coated glass lenses, brass micro-gears, and aperture blades in soft studio rim lighting.',
        preview: zeroImage('nano_banana_2_1_camera_knolling.webp'),
        thinkingLevel: 'HIGH',
      },
      {
        title: 'Swiss International Typographic poster',
        prompt:
          'A Swiss International Typographic Style exhibition poster for "NANO BANANA 2.1". Top title in asymmetric bold Helvetica, date "OCTOBER", location "Platform 37" in London. Strict geometric grid with yellow, black, and green minimalist abstract geometric shapes.',
        preview: zeroImage('nano_banana_2_1_swiss_poster.webp'),
        thinkingLevel: 'HIGH',
      },
    ],
    matches: (model) => model.id.includes('nano-banana-2.1'),
  },
  {
    id: 'nano-banana-lite',
    kind: 'images',
    family: 'Nano Banana 2 Lite',
    title: 'Generate images with {model}',
    subtitle:
      'Ultra-low latency, cost-efficient image generation and editing for quick recolors, stickers, and background swaps at scale.',
    starters: [
      {
        title: 'Turn a pet photo into a die-cut sticker',
        prompt:
          'Turn the corgi in this photo into a glossy die-cut vinyl sticker with a thick white border and a soft drop shadow, in a simple cartoon style that keeps its markings, red bandana, and happy expression recognizable.',
        images: [zeroImage('nbl-corgi-ref.webp')],
        thinkingLevel: 'MINIMAL',
      },
      {
        title: 'Clean up a product photo for a marketplace listing',
        prompt:
          'Remove the kitchen clutter behind this gooseneck kettle and place it on a seamless warm-white studio backdrop with a soft contact shadow. Keep its shape, matte charcoal finish, and oak handle exactly the same, centered in 1:1.',
        images: [zeroImage('nbl-kettle-ref.webp')],
        thinkingLevel: 'MINIMAL',
      },
      {
        title: 'Shoot a 21:9 web hero with room for a headline',
        prompt:
          'A 21:9 website hero photo for a smoothie bar: a top-down spread of mango, banana, and dragon-fruit bowls on a sunny turquoise tile counter, filling the right third and leaving clean negative space on the left for a headline.',
        thinkingLevel: 'MINIMAL',
      },
    ],
    examples: [
      {
        title: 'Banana mascot sticker sheet',
        prompt:
          'Overhead photo of a sheet of glossy die-cut vinyl stickers starring one cheerful banana mascot in six poses: waving, wearing sunglasses, skateboarding, napping on a cloud, sipping bubble tea, and giving a thumbs-up. Keep the character identical, with thick white borders and one corner peeling up, on a pastel mint cutting mat in 3:4.',
        preview: zeroImage('nbl-sticker-sheet.webp'),
        thinkingLevel: 'HIGH',
      },
      {
        title: 'Sneaker in four colorways',
        prompt:
          'A 2x2 product grid of the same unbranded retro suede-and-nylon runner in banana yellow, sage green, cobalt blue, and coral. Keep the shoe, three-quarter camera angle, and soft studio lighting identical in every panel, each on a seamless backdrop tinted to match, in 4:3.',
        preview: zeroImage('nbl-sneaker-colorways.webp'),
        thinkingLevel: 'HIGH',
      },
      {
        title: 'Corgi on a sunlit Amalfi terrace',
        prompt:
          'Place this corgi on a sunlit Amalfi Coast terrace with potted lemon trees, terracotta tiles, and the turquoise sea behind it. Keep its markings, pose, and red bandana unchanged, and match the warm late-afternoon light on its fur.',
        preview: zeroImage('nbl-corgi-amalfi.webp'),
        images: [zeroImage('nbl-corgi-ref.webp')],
        thinkingLevel: 'MINIMAL',
      },
    ],
    matches: (model) =>
      model.id.includes('flash-lite-image') || model.id.includes('nano-banana-lite'),
  },
  {
    id: 'nano-banana-images',
    kind: 'images',
    family: 'Nano Banana',
    title: 'Generate images with {model}',
    subtitle:
      'Fast conversational image generation, character-consistent restyling, and multi-image fusion.',
    starters: [
      {
        title: 'Shoot a crimson fashion editorial in forest light',
        prompt:
          'Create a fashion editorial inspired by this reference: a vivid embroidered red dress against a blurred teal forest with natural movement and cinematic rim light.',
        images: [zeroImage('fashion.webp')],
      },
      {
        title: 'Restyle a botanical macro shot across four seasons',
        prompt:
          'Using this fern reference, keep the camera angle locked and generate a 2x2 grid showing spring morning dew, golden summer sun, amber autumn mist, and winter frost.',
        images: [zeroImage('fern.webp')],
      },
      {
        title: 'Reimagine a wave from beneath the surface',
        prompt:
          'Reimagine this surfing scene from beneath the water, looking up through the crystalline turquoise wave barrel as sunlight fractures into underwater caustics.',
        images: [zeroImage('surf.webp')],
      },
    ],
    examples: [
      {
        title: 'Isometric miniature Tokyo ramen shop',
        prompt:
          'Present a clear, 45° top-down isometric miniature 3D diorama of a two-story wooden Tokyo ramen shop at twilight. Warm amber lantern light spills onto wet cobblestones, a chef stirs steaming broth inside, and bold title text "TOKYO RAMEN · 12°C" sits centered at the top.',
        preview: zeroImage('nano_banana_ramen_diorama.webp'),
      },
      {
        title: 'Crystal perfume bottle logo fusion',
        prompt:
          'Integrate the botanical gold crest seamlessly onto a faceted crystal perfume bottle resting on wet dark volcanic slate, preserving glass refraction, surface curvature, and fine mist.',
        preview: zeroImage('nano_banana_pro_perfume_ad.webp'),
        images: [zeroImage('perfume_bottle_no_logo.png'), zeroImage('rainne_logo.png')],
      },
      {
        title: 'Tactile 3D sloth icon',
        prompt:
          'An icon representing a cute three-toed sloth hanging from a mossy branch on a clean white background, rendered in a colorful and tactile 3D vinyl-and-felt style.',
        preview: zeroImage('imagen_4_origami_sloth.webp'),
      },
    ],
    matches: (model) =>
      !model.id.includes('nano-banana-2.1') &&
      !model.id.includes('flash-lite-image') &&
      !model.id.includes('nano-banana-lite') &&
      (hasCode(model, 21) || model.id.includes('image') || model.id.includes('nano-banana')),
  },
  {
    id: 'flash-lite',
    kind: 'chat',
    family: 'Gemini Flash-Lite',
    title: 'Start building with {model}',
    subtitle:
      'Ultra-low latency, cost-efficient intelligence for high-volume extraction, classification, and instant UI generation.',
    starters: [
      {
        title: 'Classify 1,000 support tickets into strict JSON',
        prompt:
          'Design a compact, low-token system prompt and JSON schema to classify customer support messages by urgency, product area, and sentiment.',
        thinkingLevel: 'MINIMAL',
      },
      {
        title: 'Build a zero-dependency command palette',
        prompt:
          'Create a keyboard-first command palette in vanilla HTML, CSS, and JS with fuzzy filtering, shortcut badges, and instant results.',
        thinkingLevel: 'LOW',
      },
      {
        title: 'Normalize messy catalog attributes to NDJSON',
        prompt:
          'Convert unstructured e-commerce product descriptions into normalized newline-delimited JSON with canonical units, colors, and dimensions.',
        thinkingLevel: 'MINIMAL',
      },
    ],
    examples: [
      {
        title: 'Playglow Pixel Arcade',
        prompt:
          'Answer four playful questions and play your own custom pixel-art game on a 3D arcade cabinet skinned just for you.',
        preview: 'https://www.gstatic.com/aistudio-static/applets/pixel-arcade/image.png',
        thinkingLevel: 'MINIMAL',
      },
      {
        title: 'Shoebox Receipts to JSON',
        prompt:
          'Drop a pile of crumpled receipts to extract every vendor, line item, tax, and total into clean structured JSON and CSV.',
        preview: 'https://www.gstatic.com/aistudio-static/applets/shoebox-receipts/image.png',
        thinkingLevel: 'MINIMAL',
      },
      {
        title: 'Fresnel Code Auditor',
        prompt:
          'Audit web pages and source files for performance, accessibility, and structural improvements with instant visual feedback.',
        preview: 'https://www.gstatic.com/aistudio-static/applets/website-checker/image.png',
        thinkingLevel: 'LOW',
      },
    ],
    matches: (model) =>
      model.id.startsWith('gemini-') &&
      model.id.includes('flash-lite') &&
      !model.id.includes('image'),
  },
  {
    id: 'flash-chat',
    kind: 'chat',
    family: 'Gemini Flash',
    title: 'Start building with {model}',
    subtitle:
      'Fast, frontier-class multimodal intelligence built for responsive apps and high-throughput workflows.',
    starters: [
      {
        title: 'Turn a hand-drawn sketch into a live UI',
        prompt:
          'Build a responsive interactive dashboard from a wireframe description with live filters, keyboard shortcuts, and clean CSS grid layout.',
        thinkingLevel: 'LOW',
      },
      {
        title: 'Extract structured JSON from messy receipts',
        prompt:
          'Design a strict JSON Schema and extraction prompt that normalizes multi-currency receipts, line items, tax rates, and confidence scores.',
        thinkingLevel: 'MINIMAL',
      },
      {
        title: 'Compare live flight & weather disruptions',
        prompt:
          'Search current major airport delays and severe weather advisories, then summarize the top affected routes in a structured table.',
        thinkingLevel: 'LOW',
        search: true,
      },
    ],
    examples: [
      {
        title: 'Neon Snake 3D',
        prompt:
          'Build a fast-paced neon grid snake game with smooth trail particles, glowing collectible orbs, and a live leaderboard.',
        preview: 'https://www.gstatic.com/aistudio/starter-apps/thumbnails/Neon_Snake.png',
        thinkingLevel: 'LOW',
      },
      {
        title: 'Lumina Shader Studio',
        prompt:
          'Design an interactive festival guide with animated stage lineups, artist previews, and a custom schedule builder.',
        preview: 'https://www.gstatic.com/aistudio/starter-apps/thumbnails/lumina.png',
        thinkingLevel: 'LOW',
      },
      {
        title: 'Geo Map Stylist',
        prompt:
          'Create an interactive map styling tool that generates custom color palettes and landmark themes from a mood description.',
        preview: 'https://www.gstatic.com/aistudio/starter-apps/thumbnails/map-styling-sandbox.png',
        thinkingLevel: 'LOW',
      },
    ],
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
    title: 'Start building with {model}',
    subtitle: 'Deep reasoning, long-context synthesis, and production software engineering.',
    starters: [
      {
        title: 'Build a WebGL2 hydraulic erosion simulator',
        prompt:
          'Write a single-file HTML/WebGL2 terrain generator with heightmap erosion, orbit camera controls, and live shader sliders.',
        thinkingLevel: 'HIGH',
      },
      {
        title: 'Audit an async task queue for race conditions',
        prompt:
          'Design a bounded TypeScript worker pool with cancellation tokens, exponential backoff, and deterministic unit tests.',
        thinkingLevel: 'HIGH',
        codeExecution: true,
      },
      {
        title: 'Compare solid-state battery breakthroughs',
        prompt:
          'Search recent solid-state electrolyte papers and compare ionic conductivity, dendrite suppression, and cycle life in a table.',
        thinkingLevel: 'HIGH',
        search: true,
      },
    ],
    examples: [
      {
        title: 'Voxel Toy Box',
        prompt:
          'Create an interactive 3D voxel building sandbox with physics, destructible blocks, and custom palette tools.',
        preview: 'https://www.gstatic.com/aistudio/starter-apps/thumbnails/voxel_toy_box.png',
        thinkingLevel: 'HIGH',
      },
      {
        title: 'Shader Pilot',
        prompt:
          'Build an interactive real-time GLSL fragment shader playground with live uniforms and camera controls.',
        preview: 'https://www.gstatic.com/aistudio/starter-apps/thumbnails/shader_pilot.png',
        thinkingLevel: 'HIGH',
      },
      {
        title: 'Research Visualization',
        prompt:
          'Turn a dense technical research topic into an interactive visual explainer with manipulable diagrams.',
        preview:
          'https://www.gstatic.com/aistudio/starter-apps/thumbnails/research_visualization.png',
        thinkingLevel: 'HIGH',
      },
    ],
    matches: (model) =>
      model.id.startsWith('gemini-') && model.id.includes('pro') && !model.id.includes('image'),
  },
  {
    id: 'flagship-chat',
    kind: 'chat',
    family: 'Gemini',
    title: 'Chat with {model}',
    subtitle: 'Ask, reason, and build. Start with a prompt or remix an app from the gallery.',
    docs: 'https://aistudio.google.com/docs/latest-model',
    starters: [
      {
        title: 'Estimate Jet Stream Speed',
        prompt:
          "Look up today's nonstop flight times between New York and London in both directions and use Python to estimate the speed of the jet stream.",
        thinkingLevel: 'HIGH',
        search: true,
        codeExecution: true,
      },
      {
        title: 'Compare National Park Seasons and Permits',
        prompt:
          'Compare visitor seasons and permit rules for Zion, Yosemite, and Glacier National Parks to pick the quietest week in May.',
        thinkingLevel: 'HIGH',
        search: true,
      },
      {
        title: 'Understand why GPS needs relativity',
        prompt:
          "Explain how GPS satellites correct for Einstein's relativity so a phone map does not drift by miles every day.",
        thinkingLevel: 'HIGH',
      },
    ],
    examples: [
      {
        title: 'Chess 3D',
        prompt:
          'Interactive 3D Gothic Cathedral Wizard Chess with sculpted stone pieces, cinematic melee captures, stone shatter physics, and single-player AI.',
        preview: 'https://www.gstatic.com/aistudio-static/applets/wizard-chess-3d/image.png',
        thinkingLevel: 'HIGH',
      },
      {
        title: 'Pocket World Terrarium',
        prompt:
          'A cozy miniature world builder in Three.js featuring a faceted terrarium planet suspended inside a glass orb on a walnut stand.',
        preview: 'https://www.gstatic.com/aistudio-static/applets/pocket-world-terrarium/image.png',
        thinkingLevel: 'HIGH',
      },
      {
        title: 'Desk Racer',
        prompt: 'Miniature desk coaster time-trial racing game in Three.js',
        preview: 'https://www.gstatic.com/aistudio-static/applets/desk-racer/image.png',
        thinkingLevel: 'HIGH',
      },
    ],
    matches: (model) => model.id.startsWith('gemini-') && !model.id.includes('pro'),
  },
]

// zeroConfigFor 返回模型的空态配置，没有匹配的系列时为 undefined
export function zeroConfigFor(model: Model | undefined): ZeroConfig | undefined {
  return model === undefined ? undefined : zeroConfigs.find((config) => config.matches(model))
}
