import { computed, shallowRef } from 'vue'
import { getAppRuntimeContext } from '@runtime/context'
import type { DialogLayerState } from '@runtime/context'

// Every BaseDialog is teleported to body. Order by opening time rather than
// component mount order, so a verification dialog can cover its caller.
const standaloneLayers = shallowRef<DialogLayerState[]>([])
const layerGap = 10

export function useDialogStack(minimum: () => number) {
  const runtime = getAppRuntimeContext()
  const layers = runtime ? (runtime.dialogLayers ??= shallowRef<DialogLayerState[]>([])) : standaloneLayers
  const entry: DialogLayerState = { minimum }
  const isTopmost = computed(() => layers.value.at(-1) === entry)
  const zIndex = computed(() => {
    let current = 0
    for (const layer of layers.value) {
      current = Math.max(layer.minimum(), current + layerGap)
      if (layer === entry) return current
    }
    return minimum()
  })

  function open() {
    if (typeof document === 'undefined') return
    if (!layers.value.includes(entry)) layers.value = [...layers.value, entry]
    document.body.classList.add('modal-open')
  }

  function close() {
    if (typeof document === 'undefined') return
    layers.value = layers.value.filter(layer => layer !== entry)
    if (!layers.value.length) document.body.classList.remove('modal-open')
  }

  return { isTopmost, zIndex, open, close }
}
