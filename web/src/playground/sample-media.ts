import type { TranslationKey } from '@/i18n'

// SampleVideo 是官网 Sample Media 中的一个 YouTube 示例视频
export interface SampleVideo {
  id: string
  name: string
  duration: string
  thumbnail: string
}

// SampleCategory 是官网 Sample Media 中的一组示例图片
export interface SampleCategory {
  name: string
  label: TranslationKey
  images: string[]
}

// sampleVideos 与官网 Sample Media 的视频顺序一致
export const sampleVideos: SampleVideo[] = [
  {
    id: 'rOVtjJkqtiA',
    name: 'Sherlock Jr. (1924) - Full Movie',
    duration: '44:07',
    thumbnail: 'https://www.gstatic.com/aistudio/Buster_Keaton_thumbnail.png',
  },
  {
    id: 'awe3e7gCur0',
    name: 'Sherlock Jr. (1924) - 10 Min Clip',
    duration: '10:00',
    thumbnail: 'https://www.gstatic.com/aistudio/SherlockJr_10min_thumbnail.png',
  },
  {
    id: 'dRg19bVPwkg',
    name: 'American Museum of Natural History Tour - 30 Min',
    duration: '30:00',
    thumbnail: 'https://www.gstatic.com/aistudio/AMHN_30min_thumbnail.png',
  },
  {
    id: 'UxJJiO6zNSo',
    name: 'American Museum of Natural History Tour - 10 Min',
    duration: '10:00',
    thumbnail: 'https://www.gstatic.com/aistudio/AMHN_10min_thumbnail.png',
  },
  {
    id: 'tv70xSSsMx0',
    name: 'American Museum of Natural History Tour - 5 Min',
    duration: '5:00',
    thumbnail: 'https://www.gstatic.com/aistudio/AMHN_5min_thumbnail.png',
  },
]

// sampleCategory 返回官网一组五张示例图片
function sampleCategory(name: string, label: TranslationKey): SampleCategory {
  return {
    name,
    label,
    images: [1, 2, 3, 4, 5].map(
      (index) => `https://www.gstatic.com/aistudio/sample-images/${name}${index}.jpg`,
    ),
  }
}

// sampleCategories 与官网 Sample Media 的图片分类顺序一致
export const sampleCategories: SampleCategory[] = [
  sampleCategory('animal', 'pg.sampleAnimal'),
  sampleCategory('architecture', 'pg.sampleArchitecture'),
  sampleCategory('nature', 'pg.sampleNature'),
  sampleCategory('flower', 'pg.sampleFlower'),
  sampleCategory('food', 'pg.sampleFood'),
  sampleCategory('objects', 'pg.sampleObjects'),
  sampleCategory('transportation', 'pg.sampleTransportation'),
  sampleCategory('space', 'pg.sampleSpace'),
]
