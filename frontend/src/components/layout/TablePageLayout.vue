<template>
  <div class="table-page-layout" :class="{ 'mobile-mode': isMobile }">
    <!-- 固定区域：操作按钮（页头在控制台里会移到顶栏，空了就不占位） -->
    <div v-if="$slots.actions" class="layout-section-fixed">
      <slot name="actions" />
    </div>

    <!-- 固定区域：搜索和过滤器 -->
    <div v-if="$slots.filters" class="layout-section-fixed">
      <slot name="filters" />
    </div>

    <!-- 滚动区域：表格 -->
    <div class="layout-section-scrollable">
      <div class="card table-scroll-container">
        <slot name="table" />
      </div>
    </div>

    <!-- 固定区域：分页器 -->
    <div v-if="$slots.pagination" class="layout-section-fixed">
      <slot name="pagination" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

const isMobile = ref(false)

const checkMobile = () => {
  isMobile.value = window.innerWidth < 1024
}

onMounted(() => {
  checkMobile()
  window.addEventListener('resize', checkMobile)
})

onUnmounted(() => {
  window.removeEventListener('resize', checkMobile)
})
</script>

<style scoped>
@reference "../../style.css";
/* 桌面端：填满控制台面板的剩余高度，表格在里面滚动 */
.table-page-layout {
  @apply flex flex-col gap-4;
  flex: 1 1 0;
  min-height: 0;
}

.layout-section-fixed {
  @apply flex-shrink-0;
}

/* The page header teleports into the navbar; an emptied slot must not leave a gap. */
.layout-section-fixed:empty {
  display: none;
}

.layout-section-scrollable {
  @apply flex-1 min-h-0 flex flex-col;
}

/* 表格滚动容器：发丝边框，无阴影 */
.table-scroll-container {
  @apply flex flex-col overflow-hidden h-full border border-default;
  border-radius: calc(var(--ui-radius) * 2);
  background: var(--signal-page-surface, var(--ui-bg));
  box-shadow: none;
}

.table-scroll-container :deep(.table-wrapper) {
  @apply flex-1 overflow-x-auto overflow-y-auto;
  /* 确保横向滚动条显示在最底部 */
  scrollbar-gutter: stable;
}

.table-scroll-container :deep(table) {
  @apply w-full;
  min-width: max-content; /* 关键：确保表格宽度根据内容撑开，从而触发横向滚动 */
  display: table; /* 使用标准 table 布局以支持 sticky 列 */
}

.table-scroll-container :deep(thead) {
  @apply bg-muted;
}

.table-scroll-container :deep(th) {
  @apply px-4 py-3 text-left text-sm font-medium text-highlighted border-b border-default;
}

.table-scroll-container :deep(td) {
  @apply px-4 py-3 text-sm text-default border-b border-default;
}

/* 移动端：恢复正常滚动，由面板整体滚动 */
.table-page-layout.mobile-mode {
  flex: none;
  height: auto;
}

.table-page-layout.mobile-mode .table-scroll-container {
  @apply h-auto overflow-visible border-none shadow-none bg-transparent;
}

.table-page-layout.mobile-mode .layout-section-scrollable {
  @apply flex-none min-h-fit;
}

.table-page-layout.mobile-mode .table-scroll-container :deep(table) {
  @apply flex-none;
  display: table;
  min-width: 100%;
}
</style>
