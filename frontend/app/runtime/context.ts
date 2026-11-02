import { tryUseNuxtApp } from '#app'
import type { AppRuntimeContext } from '../../src/runtime/context'

/** Never retain a NuxtApp outside its request. Async Nuxt context is request-local. */
export function getAppRuntimeContext(): AppRuntimeContext | undefined {
  return tryUseNuxtApp()?.$legacyContext as AppRuntimeContext | undefined
}
