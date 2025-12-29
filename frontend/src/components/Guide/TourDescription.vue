<template>
  <div class="tour-description">
    <!-- 主要段落 -->
    <p v-if="mainText" class="main-text">{{ mainText }}</p>

    <!-- 特性列表 -->
    <div v-if="features && features.length > 0" class="features-section">
      <p v-if="featuresTitle" class="section-title">{{ featuresTitle }}</p>
      <ul class="features-list">
        <li v-for="(feature, index) in features" :key="index">
          <span v-if="feature.icon" class="feature-icon">{{ feature.icon }}</span>
          <span v-if="feature.label" class="feature-label">{{ feature.label }}</span>
          <span class="feature-text">{{ feature.text }}</span>
        </li>
      </ul>
    </div>

    <!-- 提示框 -->
    <div v-if="tip" :class="['tip-box', `tip-${tip.type || 'info'}`]">
      <span v-if="tip.label" class="tip-label">{{ tip.label }}</span>
      <div v-if="tip.text" class="tip-text">{{ tip.text }}</div>
      <ul v-if="tip.items && tip.items.length > 0" class="tip-list">
        <li v-for="(item, index) in tip.items" :key="index">{{ item }}</li>
      </ul>
    </div>

    <!-- 行动提示 -->
    <p v-if="action" class="action-text">{{ action }}</p>

    <!-- 额外说明 -->
    <p v-if="note" class="note-text">{{ note }}</p>
  </div>
</template>

<script setup lang="ts">
export interface TourFeature {
  icon?: string
  label?: string
  text: string
}

export interface TourTip {
  type?: 'info' | 'success' | 'warning' | 'example'
  label?: string
  text?: string
  items?: string[]
}

export interface TourDescriptionProps {
  mainText?: string
  featuresTitle?: string
  features?: TourFeature[]
  tip?: TourTip
  action?: string
  note?: string
}

defineProps<TourDescriptionProps>()
</script>

<style scoped>
.tour-description {
  line-height: 1.7;
  font-size: 14px;
}

.main-text {
  margin-bottom: 12px;
  color: #374151;
}

.section-title {
  margin-bottom: 8px;
  font-weight: 600;
  color: #1f2937;
}

.features-section {
  margin-bottom: 12px;
}

.features-list {
  margin-left: 20px;
  font-size: 13px;
}

.features-list li {
  margin-bottom: 6px;
}

.feature-icon {
  margin-right: 4px;
}

.feature-label {
  font-weight: 600;
  margin-right: 4px;
}

.tip-box {
  padding: 8px 12px;
  border-radius: 4px;
  border-left: 3px solid;
  font-size: 13px;
  margin-bottom: 12px;
}

.tip-info {
  background: #eff6ff;
  border-left-color: #3b82f6;
}

.tip-success {
  background: #f0fdf4;
  border-left-color: #10b981;
}

.tip-warning {
  background: #fef3c7;
  border-left-color: #f59e0b;
}

.tip-example {
  background: #f0fdf4;
  border-left-color: #10b981;
}

.tip-label {
  font-weight: 600;
  display: block;
  margin-bottom: 4px;
}

.tip-text {
  margin-bottom: 4px;
}

.tip-list {
  margin: 8px 0 0 16px;
}

.tip-list li {
  margin-bottom: 4px;
}

.action-text {
  margin-top: 12px;
  color: #10b981;
  font-weight: 600;
}

.note-text {
  font-size: 13px;
  color: #6b7280;
  margin-top: 8px;
}
</style>
