import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import App from './App.vue'

declare module 'vue-router' {
  // RouteMeta 中 studio 表示 API 试用页，其余路由为管理端主页面
  interface RouteMeta {
    studio?: boolean
  }
}

// studio 为 API 试用页的路由信息，路径与官网 Playground 相同
const studio = { studio: true }

// routes 为管理端主页面与 API 试用页
export const routes: RouteRecordRaw[] = [
  { path: '/', name: 'console', component: App },
  { path: '/login', name: 'login', component: App },
  {
    path: '/prompts/new_chat',
    name: 'prompt',
    component: () => import('@/playground/PlaygroundPage.vue'),
    meta: studio,
  },
  {
    path: '/prompts/new_image',
    name: 'image',
    component: () => import('@/playground/media/ImageRoute.vue'),
    meta: studio,
  },
  {
    path: '/prompts/new_video',
    name: 'video',
    component: () => import('@/playground/media/VideoPage.vue'),
    meta: studio,
  },
  {
    path: '/generate-speech',
    name: 'speech',
    component: () => import('@/playground/media/SpeechPage.vue'),
    meta: studio,
  },
  {
    path: '/new_music',
    name: 'music',
    component: () => import('@/playground/media/MusicPage.vue'),
    meta: studio,
  },
  {
    path: '/live',
    name: 'live',
    component: () => import('@/playground/media/LivePage.vue'),
    meta: studio,
  },
  {
    path: '/prompts/:id',
    name: 'saved',
    component: () => import('@/playground/PromptRoute.vue'),
    meta: studio,
  },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

export const router = createRouter({ history: createWebHistory(), routes })
