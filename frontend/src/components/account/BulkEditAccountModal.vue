<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.bulkEdit.title')"
    width="wide"
    @close="handleClose"
  >
    <form id="bulk-edit-account-form" class="space-y-5" @submit.prevent="() => handleSubmit()">
      <!-- Info -->
      <UAlert color="neutral" variant="subtle" icon="i-lucide-info" :description="t('admin.accounts.bulkEdit.selectionInfo', { count: targetMode === 'filtered' ? targetPreviewCount : accountIds.length })" />

      <!-- Mixed platform warning -->
      <UAlert v-if="isMixedPlatform" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.accounts.bulkEdit.mixedPlatformWarning', { platforms: targetSelectedPlatforms.join(', ') })" />

      <!-- Base URL (API Key only) -->
      <div class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <label
            id="bulk-edit-base-url-label"
            class="block text-sm font-medium text-default"
            for="bulk-edit-base-url-enabled"
          >
            {{ t('admin.accounts.baseUrl') }}
          </label>
          <UCheckbox v-model="enableBaseUrl" id="bulk-edit-base-url-enabled" aria-controls="bulk-edit-base-url" />
        </div>
        <UInput v-model="baseUrl" id="bulk-edit-base-url" type="text" :disabled="!enableBaseUrl" :placeholder="t('admin.accounts.bulkEdit.baseUrlPlaceholder')" aria-labelledby="bulk-edit-base-url-label" class="w-full" />
        <GrokBaseUrlPresets
          v-if="allTargetsGrok"
          class="mt-2"
          @select="baseUrl = $event; enableBaseUrl = true"
        />
        <p class="mt-1.5 text-xs text-muted">
          {{ t('admin.accounts.bulkEdit.baseUrlNotice') }}
        </p>
      </div>

      <!-- Model restriction -->
      <div class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <label
            id="bulk-edit-model-restriction-label"
            class="block text-sm font-medium text-default"
            for="bulk-edit-model-restriction-enabled"
          >
            {{ t('admin.accounts.modelRestriction') }}
          </label>
          <UCheckbox v-model="enableModelRestriction" id="bulk-edit-model-restriction-enabled" aria-controls="bulk-edit-model-restriction-body" />
        </div>

        <div
          id="bulk-edit-model-restriction-body"
          :class="!enableModelRestriction && 'pointer-events-none opacity-50'"
          role="group"
          aria-labelledby="bulk-edit-model-restriction-label"
        >
          <UAlert v-if="isOpenAIModelRestrictionDisabled" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.accounts.openai.modelRestrictionDisabledByPassthrough')" />

          <template v-else>
            <!-- Mode Toggle -->
            <div class="mb-4 flex gap-1 rounded-lg bg-elevated p-1">
              <UButton
                :color="modelRestrictionMode === 'whitelist' ? 'primary' : 'neutral'"
                :variant="modelRestrictionMode === 'whitelist' ? 'solid' : 'ghost'"
                size="sm"
                icon="i-lucide-circle-check"
                block
                class="flex-1"
                type="button"
                @click="modelRestrictionMode = 'whitelist'"
              >{{ t('admin.accounts.modelWhitelist') }}</UButton>
              <UButton
                :color="modelRestrictionMode === 'mapping' ? 'primary' : 'neutral'"
                :variant="modelRestrictionMode === 'mapping' ? 'solid' : 'ghost'"
                size="sm"
                icon="i-lucide-arrow-left-right"
                block
                class="flex-1"
                type="button"
                @click="modelRestrictionMode = 'mapping'"
              >{{ t('admin.accounts.modelMapping') }}</UButton>
            </div>

            <!-- Whitelist Mode -->
            <div v-if="modelRestrictionMode === 'whitelist'">
              <UAlert color="neutral" variant="subtle" icon="i-lucide-info" :description="t('admin.accounts.selectAllowedModels')" class="mb-3" />

              <ModelWhitelistSelector
                v-model="allowedModels"
                :platforms="targetSelectedPlatforms"
              />

              <p class="text-xs text-muted">
                {{ t('admin.accounts.selectedModels', { count: allowedModels.length }) }}
                <span v-if="allowedModels.length === 0">{{
                  t('admin.accounts.supportsAllModels')
                }}</span>
              </p>
            </div>

            <!-- Mapping Mode -->
            <div v-else>
              <UAlert color="neutral" variant="subtle" icon="i-lucide-info" :description="t('admin.accounts.mapRequestModels')" class="mb-3" />

              <!-- Model Mapping List -->
              <div v-if="modelMappings.length > 0" class="mb-3 space-y-2">
                <div
                  v-for="(mapping, index) in modelMappings"
                  :key="index"
                  class="flex items-center gap-2"
                >
                  <UInput
                    v-model="mapping.from"
                    type="text"
                    :placeholder="t('admin.accounts.requestModel')"
                    class="flex-1"
                  />
                  <UIcon name="i-lucide-arrow-right" class="size-4 flex-shrink-0 text-muted" />
                  <UInput
                    v-model="mapping.to"
                    type="text"
                    :placeholder="t('admin.accounts.actualModel')"
                    class="flex-1"
                  />
                  <UButton
                    color="error"
                    variant="ghost"
                    square
                    size="sm"
                    icon="i-lucide-trash-2"
                    :aria-label="t('admin.accounts.formActions.remove')"
                    type="button"
                    @click="removeModelMapping(index)"
                  />
                </div>
              </div>

              <UButton color="neutral" variant="soft" block icon="i-lucide-plus" class="mb-3" type="button" @click="addModelMapping">{{ t('admin.accounts.addMapping') }}</UButton>

              <!-- Quick Add Buttons -->
              <div class="flex flex-wrap gap-2">
                <UButton
                  color="neutral"
                  variant="soft"
                  size="xs"
                  v-for="preset in filteredPresets"
                  :key="preset.label"
                  type="button"
                  @click="addPresetMapping(preset.from, preset.to)"
                >
                  + {{ preset.label }}
                </UButton>
              </div>
            </div>
          </template>
        </div>
      </div>

      <!-- Proxy -->
      <div class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <label
            id="bulk-edit-proxy-label"
            class="block text-sm font-medium text-default"
            for="bulk-edit-proxy-enabled"
          >
            {{ t('admin.accounts.proxy') }}
          </label>
          <UCheckbox v-model="enableProxy" id="bulk-edit-proxy-enabled" aria-controls="bulk-edit-proxy-body" />
        </div>
        <div id="bulk-edit-proxy-body" :class="!enableProxy && 'pointer-events-none opacity-50'">
          <ProxySelector
            v-model="proxyId"
            :proxies="proxies"
            aria-labelledby="bulk-edit-proxy-label"
          />
        </div>
      </div>

      <!-- Concurrency & Priority -->
      <div class="grid grid-cols-2 gap-4 pt-2 lg:grid-cols-4">
        <div>
          <div class="mb-3 flex items-center justify-between">
            <label
              id="bulk-edit-concurrency-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-concurrency-enabled"
            >
              {{ t('admin.accounts.concurrency') }}
            </label>
            <UCheckbox v-model="enableConcurrency" id="bulk-edit-concurrency-enabled" aria-controls="bulk-edit-concurrency" />
          </div>
          <UInput v-model.number="concurrency" id="bulk-edit-concurrency" type="number" min="1" :disabled="!enableConcurrency" aria-labelledby="bulk-edit-concurrency-label" @input="concurrency = Math.max(1, concurrency || 1)" class="w-full" />
        </div>
        <div>
          <div class="mb-3 flex items-center justify-between">
            <label
              id="bulk-edit-load-factor-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-load-factor-enabled"
            >
              {{ t('admin.accounts.loadFactor') }}
            </label>
            <UCheckbox v-model="enableLoadFactor" id="bulk-edit-load-factor-enabled" aria-controls="bulk-edit-load-factor" />
          </div>
          <UInput v-model.number="loadFactor" id="bulk-edit-load-factor" type="number" min="1" :disabled="!enableLoadFactor" aria-labelledby="bulk-edit-load-factor-label" @input="loadFactor = (loadFactor &amp;&amp; loadFactor >= 1) ? loadFactor : null" class="w-full" />
          <p class="mt-1.5 text-xs text-muted">{{ t('admin.accounts.loadFactorHint') }}</p>
        </div>
        <div>
          <div class="mb-3 flex items-center justify-between">
            <label
              id="bulk-edit-priority-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-priority-enabled"
            >
              {{ t('admin.accounts.priority') }}
            </label>
            <UCheckbox v-model="enablePriority" id="bulk-edit-priority-enabled" aria-controls="bulk-edit-priority" />
          </div>
          <UInput v-model.number="priority" id="bulk-edit-priority" type="number" min="0" step="1" :disabled="!enablePriority" aria-labelledby="bulk-edit-priority-label" class="w-full" />
        </div>
        <div>
          <div class="mb-3 flex items-center justify-between">
            <label
              id="bulk-edit-rate-multiplier-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-rate-multiplier-enabled"
            >
              {{ t('admin.accounts.billingRateMultiplier') }}
            </label>
            <UCheckbox v-model="enableRateMultiplier" id="bulk-edit-rate-multiplier-enabled" aria-controls="bulk-edit-rate-multiplier" />
          </div>
          <UInput v-model.number="rateMultiplier" id="bulk-edit-rate-multiplier" type="number" min="0" step="0.01" :disabled="!enableRateMultiplier" aria-labelledby="bulk-edit-rate-multiplier-label" class="w-full" />
          <p class="mt-1.5 text-xs text-muted">{{ t('admin.accounts.billingRateMultiplierHint') }}</p>
          <p
            v-if="enableRateMultiplier"
            class="mt-2 flex items-start gap-1 text-xs text-warning"
            data-testid="bulk-rate-sync-warning"
          >
            <Icon name="exclamationTriangle" size="xs" class="mt-0.5 flex-shrink-0" />
            <span>{{ t('admin.accounts.bulkEdit.rateSyncWarning') }}</span>
          </p>
        </div>
      </div>

      <!-- Status -->
      <div class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <label
            id="bulk-edit-status-label"
            class="block text-sm font-medium text-default"
            for="bulk-edit-status-enabled"
          >
            {{ t('common.status') }}
          </label>
          <UCheckbox v-model="enableStatus" id="bulk-edit-status-enabled" aria-controls="bulk-edit-status" />
        </div>
        <div id="bulk-edit-status" :class="!enableStatus && 'pointer-events-none opacity-50'">
          <Select
            v-model="status"
            :options="statusOptions"
            aria-labelledby="bulk-edit-status-label"
          />
        </div>
      </div>

      <UCollapsible :unmount-on-hide="false">
        <UButton type="button" color="neutral" variant="ghost" :label="t('admin.accounts.advancedOptions')" trailing-icon="i-lucide-chevron-down" class="group -ms-2.5 font-semibold text-highlighted" :ui="{ trailingIcon: 'transition-transform duration-200 group-data-[state=open]:rotate-180' }" />
        <template #content>
          <div class="space-y-6 pt-4">
            <div
        v-if="allOpenAIPassthroughCapable"
        class="pt-2"
      >
        <div class="mb-3 flex items-center justify-between">
          <div class="flex-1 pr-4">
            <label
              id="bulk-edit-openai-passthrough-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-openai-passthrough-enabled"
            >
              {{ t('admin.accounts.openai.oauthPassthrough') }}
            </label>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.openai.oauthPassthroughDesc') }}
            </p>
          </div>
          <UCheckbox v-model="enableOpenAIPassthrough" id="bulk-edit-openai-passthrough-enabled" aria-controls="bulk-edit-openai-passthrough-body" />
        </div>
        <div
          id="bulk-edit-openai-passthrough-body"
          :class="!enableOpenAIPassthrough && 'pointer-events-none opacity-50'"
          role="group"
          aria-labelledby="bulk-edit-openai-passthrough-label"
        >
          <Toggle v-model="openaiPassthroughEnabled" id="bulk-edit-openai-passthrough-toggle" />
        </div>
      </div>

      <!-- OpenAI Codex namespace 工具摊平（兼容开关，仅 OAuth） -->
      <div
        v-if="allOpenAIOAuthOnly"
        class="pt-2"
      >
        <div class="mb-3 flex items-center justify-between">
          <div class="flex-1 pr-4">
            <label
              id="bulk-edit-openai-flatten-namespaces-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-openai-flatten-namespaces-enabled"
            >
              {{ t('admin.accounts.openai.flattenNamespaces') }}
            </label>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.openai.flattenNamespacesDesc') }}
            </p>
          </div>
          <UCheckbox v-model="enableOpenAIFlattenNamespaces" id="bulk-edit-openai-flatten-namespaces-enabled" aria-controls="bulk-edit-openai-flatten-namespaces-body" />
        </div>
        <div
          id="bulk-edit-openai-flatten-namespaces-body"
          :class="!enableOpenAIFlattenNamespaces && 'pointer-events-none opacity-50'"
          role="group"
          aria-labelledby="bulk-edit-openai-flatten-namespaces-label"
        >
          <Toggle v-model="openaiFlattenNamespacesEnabled" id="bulk-edit-openai-flatten-namespaces-toggle" />
        </div>
      </div>

      <!-- OpenAI API long-context billing -->
      <div
        v-if="allOpenAIPassthroughCapable"
        class="pt-2"
      >
        <div class="mb-3 flex items-center justify-between gap-4">
          <div class="flex-1">
            <label
              id="bulk-edit-openai-long-context-billing-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-openai-long-context-billing-enabled"
            >
              {{ t('admin.accounts.openai.longContextBilling') }}
            </label>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.openai.longContextBillingDesc') }}
            </p>
          </div>
          <UCheckbox v-model="enableOpenAILongContextBilling" id="bulk-edit-openai-long-context-billing-enabled" aria-controls="bulk-edit-openai-long-context-billing-body" />
        </div>
        <div
          id="bulk-edit-openai-long-context-billing-body"
          :class="!enableOpenAILongContextBilling && 'pointer-events-none opacity-50'"
          role="group"
          aria-labelledby="bulk-edit-openai-long-context-billing-label"
        >
          <Toggle v-model="openAILongContextBillingEnabled" data-testid="bulk-edit-openai-long-context-billing-toggle" :disabled="!enableOpenAILongContextBilling" />
        </div>
        <p
          class="mt-3 rounded-lg bg-warning/10 px-3 py-2 text-xs text-warning"
          data-testid="bulk-edit-openai-long-context-shadow-hint"
        >
          {{ t('admin.accounts.bulkEdit.longContextShadowHint') }}
        </p>
      </div>

      <!-- Custom error codes -->
      <div class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <div>
            <label
              id="bulk-edit-custom-error-codes-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-custom-error-codes-enabled"
            >
              {{ t('admin.accounts.customErrorCodes') }}
            </label>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.customErrorCodesHint') }}
            </p>
          </div>
          <UCheckbox v-model="enableCustomErrorCodes" id="bulk-edit-custom-error-codes-enabled" aria-controls="bulk-edit-custom-error-codes-body" />
        </div>

        <div v-if="enableCustomErrorCodes" id="bulk-edit-custom-error-codes-body" class="space-y-3">
          <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.accounts.customErrorCodesWarning')" />

          <!-- Error Code Buttons -->
          <div class="flex flex-wrap gap-2">
            <UButton
              :color="selectedErrorCodes.includes(code.value) ? 'error' : 'neutral'"
              :variant="selectedErrorCodes.includes(code.value) ? 'soft' : 'outline'"
              size="sm"
              v-for="code in commonErrorCodes"
              :key="code.value"
              type="button"
              @click="toggleErrorCode(code.value)"
            >{{ code.value }} {{ code.label }}</UButton>
          </div>

          <!-- Manual input -->
          <div class="flex items-center gap-2">
            <UInput
              v-model="customErrorCodeInput"
              id="bulk-edit-custom-error-code-input"
              type="number"
              min="100"
              max="599"
              :placeholder="t('admin.accounts.enterErrorCode')"
              aria-labelledby="bulk-edit-custom-error-codes-label"
              @keyup.enter="addCustomErrorCode"
              class="flex-1"
            />
            <UButton color="neutral" variant="outline" type="button" @click="addCustomErrorCode" class="px-3">
              <UIcon name="i-lucide-plus" class="size-4" />
            </UButton>
          </div>

          <!-- Selected codes summary -->
          <div class="flex flex-wrap gap-1.5">
            <span
              v-for="code in selectedErrorCodes.sort((a, b) => a - b)"
              :key="code"
              class="inline-flex items-center gap-1 rounded-full bg-error/10 px-2.5 py-0.5 text-sm font-medium text-error"
            >
              {{ code }}
              <UButton
                color="neutral"
                variant="link"
                square
                size="xs"
                icon="i-lucide-x"
                :aria-label="t('admin.accounts.formActions.remove')"
                class="-me-1 p-0"
                type="button"
                @click="removeErrorCode(code)"
              />
            </span>
            <span v-if="selectedErrorCodes.length === 0" class="text-xs text-muted">
              {{ t('admin.accounts.noneSelectedUsesDefault') }}
            </span>
          </div>
        </div>
      </div>

      <!-- Intercept warmup requests (Anthropic only) -->
      <div class="pt-2">
        <div class="flex items-center justify-between">
          <div class="flex-1 pr-4">
            <label
              id="bulk-edit-intercept-warmup-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-intercept-warmup-enabled"
            >
              {{ t('admin.accounts.interceptWarmupRequests') }}
            </label>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.interceptWarmupRequestsDesc') }}
            </p>
          </div>
          <UCheckbox v-model="enableInterceptWarmup" id="bulk-edit-intercept-warmup-enabled" aria-controls="bulk-edit-intercept-warmup-body" />
        </div>
        <div v-if="enableInterceptWarmup" id="bulk-edit-intercept-warmup-body" class="mt-3">
          <Toggle v-model="interceptWarmupRequests" />
        </div>
      </div>

      <!-- Header Override (eligible API-key platforms + grok OAuth) -->
      <div v-if="allHeaderOverrideCapable" class="pt-2">
        <div class="flex items-center justify-between">
          <div class="flex-1 pr-4">
            <label
              id="bulk-edit-header-override-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-header-override-enabled"
            >
              {{ t('admin.accounts.headerOverride.title') }}
            </label>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.headerOverride.hint') }}
            </p>
          </div>
          <UCheckbox v-model="enableHeaderOverride" id="bulk-edit-header-override-enabled" aria-controls="bulk-edit-header-override-body" />
        </div>
        <div v-if="enableHeaderOverride" id="bulk-edit-header-override-body" class="mt-3 space-y-3">
          <Toggle v-model="headerOverrideEnabled" />

          <div v-if="headerOverrideEnabled" class="space-y-3">
            <UAlert color="neutral" variant="subtle" icon="i-lucide-info" :description="t('admin.accounts.headerOverride.info')" />

            <p class="text-xs text-warning">
              {{ t('admin.accounts.headerOverride.bulkReplaceHint') }}
            </p>

            <HeaderOverrideEditor
              :rows="headerOverrideRows"
              @update:rows="headerOverrideRows = $event"
            />
          </div>
          <p v-else class="text-xs text-muted">
            {{ t('admin.accounts.headerOverride.bulkDisableHint') }}
          </p>
        </div>
      </div>

      <!-- OpenAI OAuth WS mode -->
      <div v-if="allOpenAIOAuth" class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <label
            id="bulk-edit-openai-ws-mode-label"
            class="block text-sm font-medium text-default"
            for="bulk-edit-openai-ws-mode-enabled"
          >
            {{ t('admin.accounts.openai.wsMode') }}
          </label>
          <UCheckbox v-model="enableOpenAIWSMode" id="bulk-edit-openai-ws-mode-enabled" aria-controls="bulk-edit-openai-ws-mode" />
        </div>
        <div
          id="bulk-edit-openai-ws-mode"
          :class="!enableOpenAIWSMode && 'pointer-events-none opacity-50'"
        >
          <p class="mb-3 text-xs text-muted">
            {{ t('admin.accounts.openai.wsModeDesc') }}
          </p>
          <p class="mb-3 text-xs text-muted">
            {{ t(openAIWSModeConcurrencyHintKey) }}
          </p>
          <Select
            v-model="openaiOAuthResponsesWebSocketV2Mode"
            data-testid="bulk-edit-openai-ws-mode-select"
            :options="openAIWSModeOptions"
            aria-labelledby="bulk-edit-openai-ws-mode-label"
          />
        </div>
      </div>

      <!-- OpenAI OAuth Codex CLI only -->
      <div v-if="allOpenAIOAuth" class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <label
            id="bulk-edit-openai-codex-cli-only-label"
            class="block text-sm font-medium text-default"
            for="bulk-edit-openai-codex-cli-only-enabled"
          >
            {{ t('admin.accounts.openai.codexCLIOnly') }}
          </label>
          <UCheckbox v-model="enableCodexCLIOnly" id="bulk-edit-openai-codex-cli-only-enabled" aria-controls="bulk-edit-openai-codex-cli-only" />
        </div>
        <div
          id="bulk-edit-openai-codex-cli-only"
          :class="!enableCodexCLIOnly && 'pointer-events-none opacity-50'"
        >
          <p class="mb-3 text-xs text-muted">
            {{ t('admin.accounts.openai.codexCLIOnlyDesc') }}
          </p>
          <Toggle v-model="codexCLIOnlyEnabled" id="bulk-edit-openai-codex-cli-only-toggle" />
        </div>
      </div>

      <!-- OpenAI OAuth: Codex app-server -->
      <div v-if="allOpenAIOAuth" class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <label
            id="bulk-edit-openai-codex-app-server-label"
            class="block text-sm font-medium text-default"
            for="bulk-edit-openai-codex-app-server-enabled"
          >
            {{ t('admin.accounts.openai.codexCLIOnlyAppServer') }}
          </label>
          <UCheckbox v-model="enableCodexCLIOnlyAppServer" id="bulk-edit-openai-codex-app-server-enabled" aria-controls="bulk-edit-openai-codex-app-server" />
        </div>
        <div
          id="bulk-edit-openai-codex-app-server"
          :class="!enableCodexCLIOnlyAppServer && 'pointer-events-none opacity-50'"
        >
          <p class="mb-3 text-xs text-muted">
            {{ t('admin.accounts.openai.codexCLIOnlyAppServerDesc') }}
          </p>
          <Toggle v-model="codexCLIOnlyAppServerEnabled" id="bulk-edit-openai-codex-app-server-toggle" />
        </div>
      </div>

      <!-- Codex 指纹收敛模式（仅 OpenAI OAuth） -->
      <div v-if="allOpenAIOAuth" class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <label class="block text-sm font-medium text-default">{{ t('admin.accounts.openai.codexFingerprintMode') }}</label>
          <UCheckbox id="bulk-edit-openai-codex-fingerprint-mode-enabled" v-model="enableCodexFingerprintMode" />
        </div>
        <div :class="!enableCodexFingerprintMode && 'pointer-events-none opacity-50'">
          <p class="mb-2 text-xs text-muted">
            {{ t('admin.accounts.openai.codexFingerprintModeDesc') }}
          </p>
          <Select v-model="codexFingerprintMode" data-testid="bulk-codex-fingerprint-mode-select" :options="codexFingerprintModeOptions" />
        </div>
      </div>

      <!-- Upstream billing auto probe (any API-key platform) -->
      <div v-if="allBillingProbeCapable" class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <div class="flex-1 pr-4">
            <label
              id="bulk-edit-upstream-billing-auto-probe-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-upstream-billing-auto-probe-enabled"
            >
              {{ t('admin.accounts.upstreamBilling.autoProbe') }}
            </label>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.upstreamBilling.autoProbeHint') }}
            </p>
          </div>
          <UCheckbox v-model="enableUpstreamBillingAutoProbe" id="bulk-edit-upstream-billing-auto-probe-enabled" aria-controls="bulk-edit-upstream-billing-auto-probe" />
        </div>
        <div
          id="bulk-edit-upstream-billing-auto-probe"
          :class="!enableUpstreamBillingAutoProbe && 'pointer-events-none opacity-50'"
          role="group"
          aria-labelledby="bulk-edit-upstream-billing-auto-probe-label"
        >
          <Select
            v-model="upstreamBillingAutoProbeMode"
            :disabled="!enableUpstreamBillingAutoProbe"
            data-testid="bulk-edit-upstream-billing-auto-probe-select"
            :options="upstreamBillingAutoProbeOptions"
            aria-labelledby="bulk-edit-upstream-billing-auto-probe-label"
          />
        </div>
      </div>

      <!-- OpenAI API Key endpoint capabilities -->
      <div v-if="allOpenAIAPIKey" class="pt-2">
        <div class="mb-3 flex items-center justify-between gap-4">
          <div class="flex-1">
            <label
              id="bulk-edit-openai-endpoint-capabilities-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-openai-endpoint-capabilities-enabled"
            >
              {{ t('admin.accounts.openai.endpointCapabilities') }}
            </label>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.openai.endpointCapabilitiesDesc') }}
            </p>
          </div>
          <UCheckbox v-model="enableOpenAIEndpointCapabilities" id="bulk-edit-openai-endpoint-capabilities-enabled" aria-controls="bulk-edit-openai-endpoint-capabilities-body" />
        </div>
        <div
          id="bulk-edit-openai-endpoint-capabilities-body"
          :class="!enableOpenAIEndpointCapabilities && 'pointer-events-none opacity-50'"
          role="group"
          aria-labelledby="bulk-edit-openai-endpoint-capabilities-label"
        >
          <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <UCheckbox
              v-for="option in openAIEndpointCapabilityOptions"
              :key="option.value"
              :disabled="!enableOpenAIEndpointCapabilities"
              :data-testid="`bulk-edit-openai-endpoint-capability-${option.value}`"
              :model-value="openAIEndpointCapabilities.includes(option.value)"
              @update:model-value="toggleOpenAIEndpointCapability(option.value)"
              :label="option.label"
            />
          </div>
        </div>
      </div>

      <!-- OpenAI API Key Responses route -->
      <div v-if="allOpenAIAPIKey" class="pt-2">
        <div class="mb-3 flex items-center justify-between gap-4">
          <div class="flex-1">
            <label
              id="bulk-edit-openai-responses-mode-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-openai-responses-mode-enabled"
            >
              {{ t('admin.accounts.openai.responsesMode') }}
            </label>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.openai.responsesModeDesc') }}
            </p>
          </div>
          <UCheckbox v-model="enableOpenAIResponsesMode" id="bulk-edit-openai-responses-mode-enabled" aria-controls="bulk-edit-openai-responses-mode-body" />
        </div>
        <div
          id="bulk-edit-openai-responses-mode-body"
          :class="!enableOpenAIResponsesMode && 'pointer-events-none opacity-50'"
          role="group"
          aria-labelledby="bulk-edit-openai-responses-mode-label"
        >
          <Select
            v-model="openAIResponsesMode"
            :disabled="!enableOpenAIResponsesMode || !openAIResponsesModeApplicable"
            data-testid="bulk-edit-openai-responses-mode-select"
            :options="openAIResponsesModeOptions"
            aria-labelledby="bulk-edit-openai-responses-mode-label"
          />
          <p
            v-if="enableOpenAIEndpointCapabilities && !openAITextGenerationCapabilityEnabled"
            class="mt-2 rounded-lg bg-warning/10 px-3 py-2 text-xs text-warning"
            data-testid="bulk-edit-openai-responses-mode-not-applicable"
          >
            {{ t('admin.accounts.openai.responsesModeTextDisabledHint') }}
          </p>
        </div>
      </div>

      <!-- OpenAI API Key WS mode -->
      <div v-if="allOpenAIAPIKey" class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <label
            id="bulk-edit-openai-apikey-ws-mode-label"
            class="block text-sm font-medium text-default"
            for="bulk-edit-openai-apikey-ws-mode-enabled"
          >
            {{ t('admin.accounts.openai.wsMode') }}
          </label>
          <UCheckbox v-model="enableOpenAIAPIKeyWSMode" id="bulk-edit-openai-apikey-ws-mode-enabled" aria-controls="bulk-edit-openai-apikey-ws-mode" />
        </div>
        <div
          id="bulk-edit-openai-apikey-ws-mode"
          :class="!enableOpenAIAPIKeyWSMode && 'pointer-events-none opacity-50'"
        >
          <p class="mb-3 text-xs text-muted">
            {{ t('admin.accounts.openai.wsModeDesc') }}
          </p>
          <p class="mb-3 text-xs text-muted">
            {{ t(openAIAPIKeyWSModeConcurrencyHintKey) }}
          </p>
          <Select
            v-model="openaiAPIKeyResponsesWebSocketV2Mode"
            data-testid="bulk-edit-openai-apikey-ws-mode-select"
            :options="openAIWSModeOptions"
            aria-labelledby="bulk-edit-openai-apikey-ws-mode-label"
          />
        </div>
      </div>

      <!-- OpenAI Compact mode -->
      <div v-if="allOpenAIPassthroughCapable" class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <div class="flex-1 pr-4">
            <label
              id="bulk-edit-openai-compact-mode-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-openai-compact-mode-enabled"
            >
              {{ t('admin.accounts.openai.compactMode') }}
            </label>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.openai.compactModeDesc') }}
            </p>
          </div>
          <UCheckbox v-model="enableOpenAICompactMode" id="bulk-edit-openai-compact-mode-enabled" aria-controls="bulk-edit-openai-compact-mode" />
        </div>
        <div
          id="bulk-edit-openai-compact-mode"
          :class="!enableOpenAICompactMode && 'pointer-events-none opacity-50'"
        >
          <Select
            v-model="openAICompactMode"
            data-testid="bulk-edit-openai-compact-mode-select"
            :options="openAICompactModeOptions"
            aria-labelledby="bulk-edit-openai-compact-mode-label"
          />
        </div>
      </div>

      <!-- OpenAI Compact model mapping -->
      <div v-if="allOpenAIPassthroughCapable" class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <div class="flex-1 pr-4">
            <label
              id="bulk-edit-openai-compact-model-mapping-label"
              class="block text-sm font-medium text-default"
              for="bulk-edit-openai-compact-model-mapping-enabled"
            >
              {{ t('admin.accounts.openai.compactModelMapping') }}
            </label>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.openai.compactModelMappingDesc') }}
            </p>
          </div>
          <UCheckbox v-model="enableOpenAICompactModelMapping" id="bulk-edit-openai-compact-model-mapping-enabled" aria-controls="bulk-edit-openai-compact-model-mapping" />
        </div>
        <div
          id="bulk-edit-openai-compact-model-mapping"
          :class="!enableOpenAICompactModelMapping && 'pointer-events-none opacity-50'"
        >
          <div v-if="openAICompactModelMappings.length > 0" class="mb-3 space-y-2">
            <div
              v-for="(mapping, index) in openAICompactModelMappings"
              :key="index"
              class="flex items-center gap-2"
            >
              <UInput
                v-model="mapping.from"
                type="text"
                :placeholder="t('admin.accounts.fromModel')"
                data-testid="bulk-edit-openai-compact-model-mapping-input"
                class="flex-1"
              />
              <span class="text-muted">→</span>
              <UInput
                v-model="mapping.to"
                type="text"
                :placeholder="t('admin.accounts.toModel')"
                data-testid="bulk-edit-openai-compact-model-mapping-input"
                class="flex-1"
              />
              <UButton
                color="error"
                variant="ghost"
                square
                size="sm"
                icon="i-lucide-trash-2"
                :aria-label="t('admin.accounts.formActions.remove')"
                type="button"
                @click="removeOpenAICompactModelMapping(index)"
              />
            </div>
          </div>
          <UButton
            color="neutral"
            variant="soft"
            block
            icon="i-lucide-plus"
            class="mb-3"
            type="button"
            data-testid="bulk-edit-openai-compact-model-mapping-add"
            @click="addOpenAICompactModelMapping"
          >
            + {{ t('admin.accounts.addMapping') }}
          </UButton>
        </div>
      </div>

      <!-- RPM Limit (仅全部为 Anthropic OAuth/SetupToken 时显示) -->
      <div v-if="allAnthropicOAuthOrSetupToken" class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <label
            id="bulk-edit-rpm-limit-label"
            class="block text-sm font-medium text-default"
            for="bulk-edit-rpm-limit-enabled"
          >
            {{ t('admin.accounts.quotaControl.rpmLimit.label') }}
          </label>
          <UCheckbox v-model="enableRpmLimit" id="bulk-edit-rpm-limit-enabled" aria-controls="bulk-edit-rpm-limit-body" />
        </div>

        <div
          id="bulk-edit-rpm-limit-body"
          :class="!enableRpmLimit && 'pointer-events-none opacity-50'"
          role="group"
          aria-labelledby="bulk-edit-rpm-limit-label"
        >
          <div class="mb-3 flex items-center justify-between">
            <span class="text-sm text-toned">{{ t('admin.accounts.quotaControl.rpmLimit.hint') }}</span>
            <Toggle v-model="rpmLimitEnabled" />
          </div>

          <div v-if="rpmLimitEnabled" class="space-y-3">
            <UFormField :label="t('admin.accounts.quotaControl.rpmLimit.baseRpm')" :help="t('admin.accounts.quotaControl.rpmLimit.baseRpmHint')">
              <UInput
                v-model.number="bulkBaseRpm"
                type="number"
                min="1"
                max="1000"
                step="1"
                :placeholder="t('admin.accounts.quotaControl.rpmLimit.baseRpmPlaceholder')"
                class="w-full"
              />
            </UFormField>

            <div>
              <label class="mb-1.5 block text-sm font-medium text-default text-xs">{{ t('admin.accounts.quotaControl.rpmLimit.strategy') }}</label>
              <div class="flex gap-1 rounded-lg bg-elevated p-1">
                <UButton
                  :color="bulkRpmStrategy === 'tiered' ? 'primary' : 'neutral'"
                  :variant="bulkRpmStrategy === 'tiered' ? 'solid' : 'ghost'"
                  size="sm"
                  block
                  class="flex-1"
                  type="button"
                  @click="bulkRpmStrategy = 'tiered'"
                >{{ t('admin.accounts.quotaControl.rpmLimit.strategyTiered') }}</UButton>
                <UButton
                  :color="bulkRpmStrategy === 'sticky_exempt' ? 'primary' : 'neutral'"
                  :variant="bulkRpmStrategy === 'sticky_exempt' ? 'solid' : 'ghost'"
                  size="sm"
                  block
                  class="flex-1"
                  type="button"
                  @click="bulkRpmStrategy = 'sticky_exempt'"
                >{{ t('admin.accounts.quotaControl.rpmLimit.strategyStickyExempt') }}</UButton>
              </div>
            </div>

            <UFormField v-if="bulkRpmStrategy === 'tiered'" :label="t('admin.accounts.quotaControl.rpmLimit.stickyBuffer')" :help="t('admin.accounts.quotaControl.rpmLimit.stickyBufferHint')">
              <UInput
                v-model.number="bulkRpmStickyBuffer"
                type="number"
                min="1"
                step="1"
                :placeholder="t('admin.accounts.quotaControl.rpmLimit.stickyBufferPlaceholder')"
                class="w-full"
              />
            </UFormField>

            </div>
          </div>

        <!-- 用户消息限速模式（独立于 RPM 开关，始终可见） -->
        <div class="mt-4">
          <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.quotaControl.rpmLimit.userMsgQueue') }}</label>
          <p class="mt-1 text-xs text-muted mb-2">
            {{ t('admin.accounts.quotaControl.rpmLimit.userMsgQueueHint') }}
          </p>
          <div class="flex space-x-2">
            <UButton
              :color="userMsgQueueMode === opt.value ? 'primary' : 'neutral'"
              :variant="userMsgQueueMode === opt.value ? 'solid' : 'outline'"
              size="sm"
              type="button"
              v-for="opt in umqModeOptions"
              :key="opt.value"
              @click="userMsgQueueMode = userMsgQueueMode === opt.value ? null : opt.value"
            >{{ opt.label }}</UButton>
          </div>
        </div>
      </div>
          </div>
        </template>
      </UCollapsible>

      <!-- Groups -->
      <div class="pt-2">
        <div class="mb-3 flex items-center justify-between">
          <label
            id="bulk-edit-groups-label"
            class="block text-sm font-medium text-default"
            for="bulk-edit-groups-enabled"
          >
            {{ t('nav.groups') }}
          </label>
          <UCheckbox v-model="enableGroups" id="bulk-edit-groups-enabled" aria-controls="bulk-edit-groups" />
        </div>
        <div id="bulk-edit-groups" :class="!enableGroups && 'pointer-events-none opacity-50'">
          <GroupSelector
            v-model="groupIds"
            :groups="groups"
            aria-labelledby="bulk-edit-groups-label"
          />
        </div>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <UButton color="neutral" variant="outline" type="button" @click="handleClose">
          {{ t('common.cancel') }}
        </UButton>
        <UButton
          color="primary"
          variant="solid"
          type="submit"
          form="bulk-edit-account-form"
          :disabled="submitting" :loading="submitting"
        >
          {{
            submitting ? t('admin.accounts.bulkEdit.updating') : t('admin.accounts.bulkEdit.submit')
          }}
        </UButton>
      </div>
    </template>
  </BaseDialog>

  <ConfirmDialog
    :show="showMixedChannelWarning"
    :title="t('admin.accounts.mixedChannelWarningTitle')"
    :message="mixedChannelWarningMessage"
    :confirm-text="t('common.confirm')"
    :cancel-text="t('common.cancel')"
    :danger="true"
    @confirm="handleMixedChannelConfirm"
    @cancel="handleMixedChannelCancel"
  />
</template>

<script setup lang="ts">
import '@/components/admin/page/admin-page.css'
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type {
  Proxy as ProxyConfig,
  AdminGroup,
  AccountPlatform,
  AccountType,
  OpenAICompactMode,
  OpenAIEndpointCapability,
  OpenAIResponsesMode
} from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import ProxySelector from '@/components/common/ProxySelector.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import ModelWhitelistSelector from '@/components/account/ModelWhitelistSelector.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  buildModelMappingObject as buildModelMappingPayload,
  getPresetMappingsByPlatform
} from '@/composables/useModelWhitelist'
import HeaderOverrideEditor from '@/components/account/HeaderOverrideEditor.vue'
import {
  buildHeaderOverridesObject,
  isHeaderOverrideCapable,
  validateHeaderOverrideRows,
  HEADER_OVERRIDE_ENABLED_CREDENTIAL_KEY,
  HEADER_OVERRIDES_CREDENTIAL_KEY,
  type HeaderOverrideRow
} from '@/components/account/credentialsBuilder'
import GrokBaseUrlPresets from '@/components/account/GrokBaseUrlPresets.vue'
import {
  OPENAI_WS_MODE_CTX_POOL,
  OPENAI_WS_MODE_OFF,
  OPENAI_WS_MODE_PASSTHROUGH,
  OPENAI_WS_MODE_HTTP_BRIDGE,
  isOpenAIWSModeEnabled,
  resolveOpenAIWSModeConcurrencyHintKey
} from '@/utils/openaiWsMode'
import type { OpenAIWSMode } from '@/utils/openaiWsMode'
import Toggle from '@/components/common/Toggle.vue'
interface Props {
  show: boolean
  accountIds: number[]
  selectedPlatforms: AccountPlatform[]
  selectedTypes: AccountType[]
  target?: {
    mode: 'selected' | 'filtered'
    filters?: Record<string, unknown>
    previewCount?: number
    selectedPlatforms?: AccountPlatform[]
    selectedTypes?: AccountType[]
  }
  proxies: ProxyConfig[]
  groups: AdminGroup[]
}

const props = defineProps<Props>()
const emit = defineEmits<{
  close: []
  updated: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

// Platform awareness
const targetMode = computed(() => props.target?.mode ?? 'selected')
const targetPreviewCount = computed(() => props.target?.previewCount ?? props.accountIds.length)
const targetSelectedPlatforms = computed(() => props.target?.selectedPlatforms ?? props.selectedPlatforms)
const targetSelectedTypes = computed(() => props.target?.selectedTypes ?? props.selectedTypes)
// Grok 快捷端点仅在所选账号全部为 grok 平台时展示（其他平台不显示）
const allTargetsGrok = computed(
  () =>
    targetSelectedPlatforms.value.length > 0 &&
    targetSelectedPlatforms.value.every((p) => p === 'grok')
)
const isMixedPlatform = computed(() => targetSelectedPlatforms.value.length > 1)

const allOpenAIPassthroughCapable = computed(() => {
  return (
    targetSelectedPlatforms.value.length === 1 &&
    targetSelectedPlatforms.value[0] === 'openai' &&
    targetSelectedTypes.value.length > 0 &&
    targetSelectedTypes.value.every(t => t === 'oauth' || t === 'setup-token' || t === 'apikey')
  )
})

const allOpenAIOAuth = computed(() => {
  return (
    targetSelectedPlatforms.value.length === 1 &&
    targetSelectedPlatforms.value[0] === 'openai' &&
    targetSelectedTypes.value.length > 0 &&
    targetSelectedTypes.value.every(t => t === 'oauth' || t === 'setup-token')
  )
})

// 严格 OAuth（不含 setup-token）：namespace 摊平兼容开关只对 OAuth 账号生效
const allOpenAIOAuthOnly = computed(() => {
  return (
    targetSelectedPlatforms.value.length === 1 &&
    targetSelectedPlatforms.value[0] === 'openai' &&
    targetSelectedTypes.value.length > 0 &&
    targetSelectedTypes.value.every(t => t === 'oauth')
  )
})

const allOpenAIAPIKey = computed(() => {
  return (
    targetSelectedPlatforms.value.length === 1 &&
    targetSelectedPlatforms.value[0] === 'openai' &&
    targetSelectedTypes.value.length > 0 &&
    targetSelectedTypes.value.every(t => t === 'apikey')
  )
})

// 上游倍率自动探测已放宽到全部 API-key 平台：只要求所选类型全为 apikey，
// 平台不限（sub2api 上游即可应答 /v1/sub2api/billing）。
const allBillingProbeCapable = computed(() => {
  return (
    targetSelectedTypes.value.length > 0 &&
    targetSelectedTypes.value.every(t => t === 'apikey')
  )
})

// 是否全部为支持请求头覆写的平台/账号类型
// 所选平台 × 所选类型的全组合均需具备覆写资格（实际选中账号是该组合的子集，
// 按交叉积判定偏保守但绝不放行不合资格的账号）
const allHeaderOverrideCapable = computed(() => {
  return (
    targetSelectedPlatforms.value.length > 0 &&
    targetSelectedTypes.value.length > 0 &&
    targetSelectedPlatforms.value.every(p =>
      targetSelectedTypes.value.every(ty => isHeaderOverrideCapable(p, ty))
    )
  )
})

// 是否全部为 Anthropic OAuth/SetupToken（RPM 配置仅在此条件下显示）
const allAnthropicOAuthOrSetupToken = computed(() => {
  return (
    targetSelectedPlatforms.value.length === 1 &&
    targetSelectedPlatforms.value[0] === 'anthropic' &&
    targetSelectedTypes.value.every(t => t === 'oauth' || t === 'setup-token')
  )
})

const filteredPresets = computed(() => {
  if (targetSelectedPlatforms.value.length === 0) return []

  const dedupedPresets = new Map<string, ReturnType<typeof getPresetMappingsByPlatform>[number]>()
  for (const platform of targetSelectedPlatforms.value) {
    for (const preset of getPresetMappingsByPlatform(platform)) {
      const key = `${preset.from}=>${preset.to}`
      if (!dedupedPresets.has(key)) {
        dedupedPresets.set(key, preset)
      }
    }
  }

  return Array.from(dedupedPresets.values())
})

// Model mapping type
interface ModelMapping {
  from: string
  to: string
}

// State - field enable flags
const enableBaseUrl = ref(false)
const enableModelRestriction = ref(false)
const enableCustomErrorCodes = ref(false)
const enableInterceptWarmup = ref(false)
const enableHeaderOverride = ref(false)
const enableProxy = ref(false)
const enableConcurrency = ref(false)
const enableLoadFactor = ref(false)
const enablePriority = ref(false)
const enableRateMultiplier = ref(false)
const enableStatus = ref(false)
const enableGroups = ref(false)
const enableOpenAIPassthrough = ref(false)
const enableOpenAIFlattenNamespaces = ref(false)
const enableOpenAILongContextBilling = ref(false)
const enableOpenAIEndpointCapabilities = ref(false)
const enableOpenAIResponsesMode = ref(false)
const enableOpenAIWSMode = ref(false)
const enableOpenAIAPIKeyWSMode = ref(false)
const enableUpstreamBillingAutoProbe = ref(false)
const enableCodexCLIOnly = ref(false)
const enableCodexCLIOnlyAppServer = ref(false)
const enableOpenAICompactMode = ref(false)
const enableOpenAICompactModelMapping = ref(false)
const enableRpmLimit = ref(false)

// State - field values
const submitting = ref(false)
const showMixedChannelWarning = ref(false)
const mixedChannelWarningMessage = ref('')
const pendingUpdatesForConfirm = ref<Record<string, unknown> | null>(null)
const baseUrl = ref('')
const modelRestrictionMode = ref<'whitelist' | 'mapping'>('whitelist')
const allowedModels = ref<string[]>([])
const modelMappings = ref<ModelMapping[]>([])
const selectedErrorCodes = ref<number[]>([])
const customErrorCodeInput = ref<number | null>(null)
const interceptWarmupRequests = ref(false)
const headerOverrideEnabled = ref(false)
const headerOverrideRows = ref<HeaderOverrideRow[]>([])
const proxyId = ref<number | null>(null)
const concurrency = ref(1)
const loadFactor = ref<number | null>(null)
const priority = ref(1)
const rateMultiplier = ref(1)
const status = ref<'active' | 'inactive'>('active')
const groupIds = ref<number[]>([])
const openaiPassthroughEnabled = ref(false)
// Codex namespace 工具摊平兼容开关（仅 OAuth），缺省关闭即原样保留
const openaiFlattenNamespacesEnabled = ref(false)
const openAILongContextBillingEnabled = ref(false)
const openAIEndpointCapabilities = ref<OpenAIEndpointCapability[]>([
  'chat_completions',
  'embeddings'
])
const openAIResponsesMode = ref<OpenAIResponsesMode>('auto')
const openaiOAuthResponsesWebSocketV2Mode = ref<OpenAIWSMode>(OPENAI_WS_MODE_OFF)
const openaiAPIKeyResponsesWebSocketV2Mode = ref<OpenAIWSMode>(OPENAI_WS_MODE_OFF)
const upstreamBillingAutoProbeMode = ref<'enabled' | 'disabled'>('enabled')
const codexCLIOnlyEnabled = ref(false)
const codexCLIOnlyAppServerEnabled = ref(false)
type CodexFingerprintMode = 'off' | 'device' | 'session' | 'full'
const enableCodexFingerprintMode = ref(false)
const codexFingerprintMode = ref<CodexFingerprintMode>('off')
const codexFingerprintModeOptions = computed(() => [
  { value: 'off' as CodexFingerprintMode, label: t('admin.accounts.openai.codexFingerprintOff') },
  { value: 'device' as CodexFingerprintMode, label: t('admin.accounts.openai.codexFingerprintDevice') },
  { value: 'session' as CodexFingerprintMode, label: t('admin.accounts.openai.codexFingerprintSession') },
  { value: 'full' as CodexFingerprintMode, label: t('admin.accounts.openai.codexFingerprintFull') },
])
const openAICompactMode = ref<OpenAICompactMode>('auto')
const openAICompactModelMappings = ref<ModelMapping[]>([])
const rpmLimitEnabled = ref(false)
const bulkBaseRpm = ref<number | null>(null)
const bulkRpmStrategy = ref<'tiered' | 'sticky_exempt'>('tiered')
const bulkRpmStickyBuffer = ref<number | null>(null)
const userMsgQueueMode = ref<string | null>(null)
const umqModeOptions = computed(() => [
  { value: '', label: t('admin.accounts.quotaControl.rpmLimit.umqModeOff') },
  { value: 'throttle', label: t('admin.accounts.quotaControl.rpmLimit.umqModeThrottle') },
  { value: 'serialize', label: t('admin.accounts.quotaControl.rpmLimit.umqModeSerialize') },
])

// Common HTTP error codes
const commonErrorCodes = [
  { value: 401, label: 'Unauthorized' },
  { value: 403, label: 'Forbidden' },
  { value: 429, label: 'Rate Limit' },
  { value: 500, label: 'Server Error' },
  { value: 502, label: 'Bad Gateway' },
  { value: 503, label: 'Unavailable' },
  { value: 529, label: 'Overloaded' }
]

const statusOptions = computed(() => [
  { value: 'active', label: t('common.active') },
  { value: 'inactive', label: t('common.inactive') }
])
const upstreamBillingAutoProbeOptions = computed(() => [
  { value: 'enabled', label: t('common.enabled') },
  { value: 'disabled', label: t('common.disabled') }
])
const isOpenAIModelRestrictionDisabled = computed(
  () =>
    allOpenAIPassthroughCapable.value &&
    enableOpenAIPassthrough.value &&
    openaiPassthroughEnabled.value
)

const openAIWSModeOptions = computed(() => [
  { value: OPENAI_WS_MODE_OFF, label: t('admin.accounts.openai.wsModeOff') },
  { value: OPENAI_WS_MODE_CTX_POOL, label: t('admin.accounts.openai.wsModeCtxPool') },
  { value: OPENAI_WS_MODE_PASSTHROUGH, label: t('admin.accounts.openai.wsModePassthrough') },
  { value: OPENAI_WS_MODE_HTTP_BRIDGE, label: t('admin.accounts.openai.wsModeHttpBridge') }
])
const openAICompactModeOptions = computed(() => [
  { value: 'auto', label: t('admin.accounts.openai.compactModeAuto') },
  { value: 'force_on', label: t('admin.accounts.openai.compactModeForceOn') },
  { value: 'force_off', label: t('admin.accounts.openai.compactModeForceOff') }
])
const openAIResponsesModeOptions = computed(() => [
  { value: 'auto', label: t('admin.accounts.openai.responsesModeAuto') },
  { value: 'force_responses', label: t('admin.accounts.openai.responsesModeForceResponses') },
  {
    value: 'force_chat_completions',
    label: t('admin.accounts.openai.responsesModeForceChatCompletions')
  }
])
const openAITextEndpointCapabilityLabel = computed(() => {
  if (openAIResponsesMode.value === 'force_responses') {
    return t('admin.accounts.openai.capabilityResponses')
  }
  if (openAIResponsesMode.value === 'force_chat_completions') {
    return t('admin.accounts.openai.capabilityChatCompletions')
  }
  return t('admin.accounts.openai.capabilityTextAuto')
})
const openAIEndpointCapabilityOptions = computed<
  Array<{ value: OpenAIEndpointCapability; label: string }>
>(() => [
  { value: 'chat_completions', label: openAITextEndpointCapabilityLabel.value },
  { value: 'embeddings', label: t('admin.accounts.openai.capabilityEmbeddings') },
  { value: 'seedance', label: 'Seedance / Ark video' }
])
const openAITextGenerationCapabilityEnabled = computed(() =>
  openAIEndpointCapabilities.value.includes('chat_completions')
)
const openAIResponsesModeApplicable = computed(
  () => !enableOpenAIEndpointCapabilities.value || openAITextGenerationCapabilityEnabled.value
)

const normalizeOpenAIEndpointCapabilities = (values: OpenAIEndpointCapability[]): OpenAIEndpointCapability[] => {
  const allowed: OpenAIEndpointCapability[] = ['chat_completions', 'embeddings', 'seedance']
  const selected = allowed.filter((value) => values.includes(value))
  return selected.length > 0 ? selected : ['chat_completions', 'embeddings']
}

const toggleOpenAIEndpointCapability = (
  capability: OpenAIEndpointCapability,
  event?: Event
) => {
  if (openAIEndpointCapabilities.value.includes(capability)) {
    if (openAIEndpointCapabilities.value.length <= 1) {
      const input = event?.target as HTMLInputElement | null
      if (input) input.checked = true
      return
    }
    openAIEndpointCapabilities.value = openAIEndpointCapabilities.value.filter(
      (value) => value !== capability
    )
    if (!openAITextGenerationCapabilityEnabled.value) {
      openAIResponsesMode.value = 'auto'
    }
    return
  }
  openAIEndpointCapabilities.value = normalizeOpenAIEndpointCapabilities([
    ...openAIEndpointCapabilities.value,
    capability
  ])
}
const openAIWSModeConcurrencyHintKey = computed(() =>
  resolveOpenAIWSModeConcurrencyHintKey(openaiOAuthResponsesWebSocketV2Mode.value)
)
const openAIAPIKeyWSModeConcurrencyHintKey = computed(() =>
  resolveOpenAIWSModeConcurrencyHintKey(openaiAPIKeyResponsesWebSocketV2Mode.value)
)

// Model mapping helpers
const addModelMapping = () => {
  modelMappings.value.push({ from: '', to: '' })
}

const removeModelMapping = (index: number) => {
  modelMappings.value.splice(index, 1)
}

const addOpenAICompactModelMapping = () => {
  openAICompactModelMappings.value.push({ from: '', to: '' })
}

const removeOpenAICompactModelMapping = (index: number) => {
  openAICompactModelMappings.value.splice(index, 1)
}

const addPresetMapping = (from: string, to: string) => {
  const exists = modelMappings.value.some((m) => m.from === from)
  if (exists) {
    appStore.showInfo(t('admin.accounts.mappingExists', { model: from }))
    return
  }
  modelMappings.value.push({ from, to })
}

// Error code helpers
const toggleErrorCode = (code: number) => {
  const index = selectedErrorCodes.value.indexOf(code)
  if (index === -1) {
    // Adding code - check for 429/529 warning
    if (code === 429) {
      if (!confirm(t('admin.accounts.customErrorCodes429Warning'))) {
        return
      }
    } else if (code === 529) {
      if (!confirm(t('admin.accounts.customErrorCodes529Warning'))) {
        return
      }
    }
    selectedErrorCodes.value.push(code)
  } else {
    selectedErrorCodes.value.splice(index, 1)
  }
}

const addCustomErrorCode = () => {
  const code = customErrorCodeInput.value
  if (code === null || code < 100 || code > 599) {
    appStore.showError(t('admin.accounts.invalidErrorCode'))
    return
  }
  if (selectedErrorCodes.value.includes(code)) {
    appStore.showInfo(t('admin.accounts.errorCodeExists'))
    return
  }
  // Check for 429/529 warning
  if (code === 429) {
    if (!confirm(t('admin.accounts.customErrorCodes429Warning'))) {
      return
    }
  } else if (code === 529) {
    if (!confirm(t('admin.accounts.customErrorCodes529Warning'))) {
      return
    }
  }
  selectedErrorCodes.value.push(code)
  customErrorCodeInput.value = null
}

const removeErrorCode = (code: number) => {
  const index = selectedErrorCodes.value.indexOf(code)
  if (index !== -1) {
    selectedErrorCodes.value.splice(index, 1)
  }
}

const buildModelMappingObject = (): Record<string, string> | null => {
  return buildModelMappingPayload(
    modelRestrictionMode.value,
    allowedModels.value,
    modelMappings.value
  )
}

const buildOpenAICompactModelMapping = (): Record<string, string> | null => {
  return buildModelMappingPayload('mapping', [], openAICompactModelMappings.value)
}

const buildUpdatePayload = (): Record<string, unknown> | null => {
  const updates: Record<string, unknown> = {}
  const credentials: Record<string, unknown> = {}
  let credentialsChanged = false
  const applyOpenAILongContextBilling =
    enableOpenAILongContextBilling.value && allOpenAIPassthroughCapable.value
  const applyOpenAIEndpointCapabilities =
    enableOpenAIEndpointCapabilities.value && allOpenAIAPIKey.value
  const applyOpenAIResponsesMode = enableOpenAIResponsesMode.value && allOpenAIAPIKey.value
  const ensureExtra = (): Record<string, unknown> => {
    if (!updates.extra) {
      updates.extra = {}
    }
    return updates.extra as Record<string, unknown>
  }

  if (enableProxy.value) {
    // 后端期望 proxy_id: 0 表示清除代理，而不是 null
    updates.proxy_id = proxyId.value === null ? 0 : proxyId.value
  }

  if (enableConcurrency.value) {
    updates.concurrency = concurrency.value
  }

  if (enableLoadFactor.value) {
    // 空值/NaN/0 时发送 0（后端约定 <= 0 表示清除）
    const lf = loadFactor.value
    updates.load_factor = (lf != null && !Number.isNaN(lf) && lf > 0) ? lf : 0
  }

  if (enablePriority.value) {
    updates.priority = priority.value
  }

  if (enableRateMultiplier.value) {
    updates.rate_multiplier = rateMultiplier.value
  }

  if (enableStatus.value) {
    updates.status = status.value
  }

  if (enableGroups.value) {
    updates.group_ids = groupIds.value
  }

  if (enableBaseUrl.value) {
    const baseUrlValue = baseUrl.value.trim()
    if (baseUrlValue) {
      credentials.base_url = baseUrlValue
      credentialsChanged = true
    }
  }

  if (enableOpenAIPassthrough.value) {
    const extra = ensureExtra()
    extra.openai_passthrough = openaiPassthroughEnabled.value
    if (!openaiPassthroughEnabled.value) {
      extra.openai_oauth_passthrough = false
    }
  }

  // 同时校验可见性：勾选后又改了目标筛选条件时，不应把该键写到非 OAuth 账号上
  if (enableOpenAIFlattenNamespaces.value && allOpenAIOAuthOnly.value) {
    const extra = ensureExtra()
    extra.openai_responses_flatten_namespaces = openaiFlattenNamespacesEnabled.value
  }

  if (applyOpenAILongContextBilling) {
    const extra = ensureExtra()
    extra.openai_long_context_billing_enabled = openAILongContextBillingEnabled.value
  }

  if (applyOpenAIEndpointCapabilities) {
    credentials.openai_capabilities =
      openAIEndpointCapabilities.value.length === 2
        ? null
        : [...openAIEndpointCapabilities.value]
    credentialsChanged = true
  }

  if (
    applyOpenAIResponsesMode ||
    (applyOpenAIEndpointCapabilities && !openAITextGenerationCapabilityEnabled.value)
  ) {
    const extra = ensureExtra()
    extra.openai_responses_mode =
      !openAIResponsesModeApplicable.value || openAIResponsesMode.value === 'auto'
        ? null
        : openAIResponsesMode.value
  }

  if (enableModelRestriction.value && !isOpenAIModelRestrictionDisabled.value) {
    // 统一使用 model_mapping 字段
    if (modelRestrictionMode.value === 'whitelist') {
      // 白名单模式：将模型转换为 model_mapping 格式（key=value）
      // 空白名单表示“支持所有模型”，需显式发送空对象以覆盖已有限制。
      const mapping: Record<string, string> = {}
      for (const m of allowedModels.value) {
        mapping[m] = m
      }
      credentials.model_mapping = mapping
      credentialsChanged = true
    } else {
      // 映射模式下空配置同样表示“支持所有模型”。
      const modelMapping = buildModelMappingObject()
      credentials.model_mapping = modelMapping ?? {}
      credentialsChanged = true
    }
  }

  if (enableCustomErrorCodes.value) {
    credentials.custom_error_codes_enabled = true
    credentials.custom_error_codes = [...selectedErrorCodes.value]
    credentialsChanged = true
  }

  if (enableInterceptWarmup.value) {
    credentials.intercept_warmup_requests = interceptWarmupRequests.value
    credentialsChanged = true
  }

  if (enableHeaderOverride.value) {
    // 后端使用 JSONB || merge 语义：关闭时显式写入 false + 空对象以清除旧配置
    credentials[HEADER_OVERRIDE_ENABLED_CREDENTIAL_KEY] = headerOverrideEnabled.value
    credentials[HEADER_OVERRIDES_CREDENTIAL_KEY] = headerOverrideEnabled.value
      ? buildHeaderOverridesObject(headerOverrideRows.value)
      : {}
    credentialsChanged = true
  }

  if (enableOpenAIWSMode.value) {
    const extra = ensureExtra()
    extra.openai_oauth_responses_websockets_v2_mode = openaiOAuthResponsesWebSocketV2Mode.value
    extra.openai_oauth_responses_websockets_v2_enabled = isOpenAIWSModeEnabled(
      openaiOAuthResponsesWebSocketV2Mode.value
    )
  }

  if (enableOpenAIAPIKeyWSMode.value) {
    const extra = ensureExtra()
    extra.openai_apikey_responses_websockets_v2_mode = openaiAPIKeyResponsesWebSocketV2Mode.value
    extra.openai_apikey_responses_websockets_v2_enabled = isOpenAIWSModeEnabled(
      openaiAPIKeyResponsesWebSocketV2Mode.value
    )
  }

  if (enableUpstreamBillingAutoProbe.value) {
    updates.upstream_billing_probe_enabled = upstreamBillingAutoProbeMode.value === 'enabled'
  }

  if (enableCodexCLIOnly.value) {
    const extra = ensureExtra()
    extra.codex_cli_only = codexCLIOnlyEnabled.value
  }

  // 子开关从属于 codex_cli_only：仅当同一次批量编辑也把父开关设为开启时才写入，
  // 与 Create/Edit 语义对齐，避免在父开关关闭的账号上写入无意义的孤立字段。
  if (
    enableCodexCLIOnlyAppServer.value &&
    enableCodexCLIOnly.value &&
    codexCLIOnlyEnabled.value
  ) {
    const extra = ensureExtra()
    extra.codex_cli_only_allow_app_server = codexCLIOnlyAppServerEnabled.value
  }

  if (enableCodexFingerprintMode.value) {
    const extra = ensureExtra()
    // off 必须显式落键，不能靠删本地键表达。批量更新走 JSONB 顶层合并
    // （extra = COALESCE(extra,'{}') || payload），删掉 payload 里的键只表示
    // "本次不更新该键"，清不掉账号上已有的 device/session/full；而且只删不写会让
    // 整个 payload 退化成 {extra:{}}，被后端 len(req.Extra) > 0 判为空更新直接 400
    // "No updates provided"（#6327）。
    //
    // Create/Edit 那两个表单可以删键，是因为它们提交完整 extra 对象、后端整体
    // SetExtra 覆盖；批量接口只合并增量键，两种持久化语义不能共用同一套写法。
    //
    // 显式 off 与不设置在读取侧完全等价：codexFingerprintModeFromExtra 对空值/
    // 非法值走 default 回落 off，对 "off" 命中同一分支，所以 #5610 定下的
    // "不显式 opt-in 就保持旧客户端身份" 不受影响；ShouldEnsureCodexFingerprintSeed-
    // ForExtraUpdates 同样只在 device/session/full 时要种子，off 不会触发。
    //
    // 与本函数里其它"关闭/清除"字段的写法一致：codex_cli_only 直接落 false，
    // load_factor 落 0，proxy_id 落 0 —— 批量路径一律用显式哨兵值，不用省略。
    extra.codex_fingerprint_mode = codexFingerprintMode.value
  }

  if (enableOpenAICompactMode.value) {
    const extra = ensureExtra()
    extra.openai_compact_mode = openAICompactMode.value
  }

  if (enableOpenAICompactModelMapping.value) {
    credentials.compact_model_mapping = buildOpenAICompactModelMapping() ?? {}
    credentialsChanged = true
  }

  // RPM limit settings (写入 extra 字段)
  if (enableRpmLimit.value) {
    const extra = ensureExtra()
    if (rpmLimitEnabled.value && bulkBaseRpm.value != null && bulkBaseRpm.value > 0) {
      extra.base_rpm = bulkBaseRpm.value
      extra.rpm_strategy = bulkRpmStrategy.value
      if (bulkRpmStickyBuffer.value != null && bulkRpmStickyBuffer.value > 0) {
        extra.rpm_sticky_buffer = bulkRpmStickyBuffer.value
      }
    } else {
      // 关闭 RPM 限制 - 设置 base_rpm 为 0，并用空值覆盖关联字段
      // 后端使用 JSONB || merge 语义，不会删除已有 key，
      // 所以必须显式发送空值来重置（后端读取时会 fallback 到默认值）
      extra.base_rpm = 0
      extra.rpm_strategy = ''
      extra.rpm_sticky_buffer = 0
    }
    updates.extra = extra
  }

  // UMQ mode（独立于 RPM 保存）
  if (userMsgQueueMode.value !== null) {
    const umqExtra = ensureExtra()
    umqExtra.user_msg_queue_mode = userMsgQueueMode.value  // '' = 清除账号级覆盖
    umqExtra.user_msg_queue_enabled = false  // 清理旧字段（JSONB merge）
  }

  if (credentialsChanged) {
    updates.credentials = credentials
  }

  return Object.keys(updates).length > 0 ? updates : null
}

const mixedChannelConfirmed = ref(false)

// 是否需要预检查：改了分组 + 全是单一的 antigravity 或 anthropic 平台
// 多平台混合的情况由 submitBulkUpdate 的 409 catch 兜底
const canPreCheck = () =>
  enableGroups.value &&
  groupIds.value.length > 0 &&
  targetSelectedPlatforms.value.length === 1 &&
  (targetSelectedPlatforms.value[0] === 'antigravity' || targetSelectedPlatforms.value[0] === 'anthropic')

const handleClose = () => {
  showMixedChannelWarning.value = false
  mixedChannelWarningMessage.value = ''
  pendingUpdatesForConfirm.value = null
  mixedChannelConfirmed.value = false
  emit('close')
}

// 预检查：提交前调接口检测，有风险就弹窗阻止，返回 false 表示需要用户确认
const preCheckMixedChannelRisk = async (built: Record<string, unknown>): Promise<boolean> => {
  if (!canPreCheck()) return true
  if (mixedChannelConfirmed.value) return true

  try {
    const result = await adminAPI.accounts.checkMixedChannelRisk({
      platform: targetSelectedPlatforms.value[0],
      group_ids: groupIds.value
    })
    if (!result.has_risk) return true

    pendingUpdatesForConfirm.value = built
    mixedChannelWarningMessage.value = result.message || t('admin.accounts.bulkEdit.failed')
    showMixedChannelWarning.value = true
    return false
  } catch (error: any) {
    appStore.showError(error.message || t('admin.accounts.bulkEdit.failed'))
    return false
  }
}

const handleSubmit = async () => {
  if (targetMode.value === 'selected' && props.accountIds.length === 0) {
    appStore.showError(t('admin.accounts.bulkEdit.noSelection'))
    return
  }

  const hasAnyFieldEnabled =
    enableBaseUrl.value ||
    enableOpenAIPassthrough.value ||
    enableOpenAIFlattenNamespaces.value ||
    (enableOpenAILongContextBilling.value && allOpenAIPassthroughCapable.value) ||
    (enableOpenAIEndpointCapabilities.value && allOpenAIAPIKey.value) ||
    (enableOpenAIResponsesMode.value && allOpenAIAPIKey.value) ||
    enableModelRestriction.value ||
    enableCustomErrorCodes.value ||
    enableInterceptWarmup.value ||
    enableHeaderOverride.value ||
    enableProxy.value ||
    enableConcurrency.value ||
    enableLoadFactor.value ||
    enablePriority.value ||
    enableRateMultiplier.value ||
    enableStatus.value ||
    enableGroups.value ||
    enableOpenAIWSMode.value ||
    enableOpenAIAPIKeyWSMode.value ||
    enableUpstreamBillingAutoProbe.value ||
    enableCodexCLIOnly.value ||
    enableCodexCLIOnlyAppServer.value ||
    enableCodexFingerprintMode.value ||
    enableOpenAICompactMode.value ||
    enableOpenAICompactModelMapping.value ||
    enableRpmLimit.value ||
    userMsgQueueMode.value !== null

  if (!hasAnyFieldEnabled) {
    appStore.showError(t('admin.accounts.bulkEdit.noFieldsSelected'))
    return
  }

  // base_url 现在也会作用于 Grok OAuth 订阅账号的转发端点；坏值会让请求期
  // 校验失败、账号请求全挂，因此保存前强制格式校验（与单账号编辑一致）。
  if (enableBaseUrl.value) {
    const trimmedBaseUrl = baseUrl.value.trim()
    if (trimmedBaseUrl && !/^https?:\/\//i.test(trimmedBaseUrl)) {
      appStore.showError(t('admin.accounts.grokCustomBaseUrl.invalid'))
      return
    }
  }

  if (enableHeaderOverride.value && headerOverrideEnabled.value) {
    // 批量保存对 header_overrides 是整键替换：开启但没有任何有效行会把所选账号的
    // 既有覆写配置静默清空，必须显式拦截（清空请走关闭开关的路径，有专门提示）
    if (!headerOverrideRows.value.some((row) => row.name.trim())) {
      appStore.showError(t('admin.accounts.headerOverride.bulkEmptyRows'))
      return
    }
    const headerError = validateHeaderOverrideRows(headerOverrideRows.value)
    if (headerError) {
      appStore.showError(t(`admin.accounts.headerOverride.${headerError}`))
      return
    }
  }

  const built = buildUpdatePayload()
  if (!built) {
    appStore.showError(t('admin.accounts.bulkEdit.noFieldsSelected'))
    return
  }

  const canContinue = await preCheckMixedChannelRisk(built)
  if (!canContinue) return

  await submitBulkUpdate(built)
}

const submitBulkUpdate = async (baseUpdates: Record<string, unknown>) => {
  // 无论是预检查确认还是 409 兜底确认，只要 mixedChannelConfirmed 为 true 就带上 flag
  const updates = mixedChannelConfirmed.value
    ? { ...baseUpdates, confirm_mixed_channel_risk: true }
    : baseUpdates

  submitting.value = true

  try {
    const res = targetMode.value === 'filtered' && props.target?.filters
      ? await adminAPI.accounts.bulkUpdate({
        filters: props.target.filters,
        ...updates
      })
      : await adminAPI.accounts.bulkUpdate(props.accountIds, updates)
    const success = res.success || 0
    const failed = res.failed || 0
    const inherited = res.long_context_inherited_count || 0

    if (success > 0 && failed === 0) {
      if (inherited > 0) {
        appStore.showSuccess(t('admin.accounts.bulkEdit.successWithInherited', {
          count: success,
          inherited
        }))
      } else {
        appStore.showSuccess(t('admin.accounts.bulkEdit.success', { count: success }))
      }
    } else if (success > 0) {
      const key = inherited > 0
        ? 'admin.accounts.bulkEdit.partialSuccessWithInherited'
        : 'admin.accounts.bulkEdit.partialSuccess'
      appStore.showError(t(key, { success, failed, inherited }))
    } else {
      appStore.showError(t('admin.accounts.bulkEdit.failed'))
    }

    if (success > 0) {
      pendingUpdatesForConfirm.value = null
      emit('updated')
      handleClose()
    }
  } catch (error: any) {
    // 兜底：多平台混合场景下，预检查跳过，由后端 409 触发确认框
    if (error.status === 409 && error.error === 'mixed_channel_warning') {
      pendingUpdatesForConfirm.value = baseUpdates
      mixedChannelWarningMessage.value = error.message
      showMixedChannelWarning.value = true
    } else if (error.reason === 'UPSTREAM_BILLING_RATE_SYNC_BULK_CONFLICT') {
      appStore.showError(t('admin.accounts.bulkEdit.rateSyncConflict', {
        count: error.metadata?.count ?? 1
      }))
    } else if (error.reason === 'OPENAI_LONG_CONTEXT_PARENT_REQUIRED') {
      appStore.showError(t('admin.accounts.bulkEdit.longContextParentRequired'))
    } else {
      appStore.showError(error.message || t('admin.accounts.bulkEdit.failed'))
      console.error('Error bulk updating accounts:', error)
    }
  } finally {
    submitting.value = false
  }
}

const handleMixedChannelConfirm = async () => {
  showMixedChannelWarning.value = false
  mixedChannelConfirmed.value = true
  if (pendingUpdatesForConfirm.value) {
    await submitBulkUpdate(pendingUpdatesForConfirm.value)
  }
}

const handleMixedChannelCancel = () => {
  showMixedChannelWarning.value = false
  pendingUpdatesForConfirm.value = null
}

// Reset form when modal closes
watch(
  () => props.show,
  (newShow) => {
    if (!newShow) {
      // Reset all enable flags
      enableBaseUrl.value = false
      enableModelRestriction.value = false
      enableCustomErrorCodes.value = false
      enableInterceptWarmup.value = false
      enableHeaderOverride.value = false
      enableProxy.value = false
      enableConcurrency.value = false
      enableLoadFactor.value = false
      enablePriority.value = false
      enableRateMultiplier.value = false
      enableStatus.value = false
      enableGroups.value = false
      enableOpenAIPassthrough.value = false
      enableOpenAIFlattenNamespaces.value = false
      enableOpenAILongContextBilling.value = false
      enableOpenAIEndpointCapabilities.value = false
      enableOpenAIResponsesMode.value = false
      enableOpenAIWSMode.value = false
      enableOpenAIAPIKeyWSMode.value = false
      enableUpstreamBillingAutoProbe.value = false
      enableCodexCLIOnly.value = false
      enableCodexCLIOnlyAppServer.value = false
      enableCodexFingerprintMode.value = false
      codexFingerprintMode.value = 'off'
      enableOpenAICompactMode.value = false
      enableOpenAICompactModelMapping.value = false
      enableRpmLimit.value = false

      // Reset all values
      baseUrl.value = ''
      openaiPassthroughEnabled.value = false
      openaiFlattenNamespacesEnabled.value = false
      openAILongContextBillingEnabled.value = false
      openAIEndpointCapabilities.value = ['chat_completions', 'embeddings']
      openAIResponsesMode.value = 'auto'
      modelRestrictionMode.value = 'whitelist'
      allowedModels.value = []
      modelMappings.value = []
      selectedErrorCodes.value = []
      customErrorCodeInput.value = null
      interceptWarmupRequests.value = false
      headerOverrideEnabled.value = false
      headerOverrideRows.value = []
      proxyId.value = null
      concurrency.value = 1
      loadFactor.value = null
      priority.value = 1
      rateMultiplier.value = 1
      status.value = 'active'
      groupIds.value = []
      openaiOAuthResponsesWebSocketV2Mode.value = OPENAI_WS_MODE_OFF
      openaiAPIKeyResponsesWebSocketV2Mode.value = OPENAI_WS_MODE_OFF
      upstreamBillingAutoProbeMode.value = 'enabled'
      codexCLIOnlyEnabled.value = false
      codexCLIOnlyAppServerEnabled.value = false
      openAICompactMode.value = 'auto'
      openAICompactModelMappings.value = []
      rpmLimitEnabled.value = false
      bulkBaseRpm.value = null
      bulkRpmStrategy.value = 'tiered'
      bulkRpmStickyBuffer.value = null
      userMsgQueueMode.value = null

      // Reset mixed channel warning state
      showMixedChannelWarning.value = false
      mixedChannelWarningMessage.value = ''
      pendingUpdatesForConfirm.value = null
      mixedChannelConfirmed.value = false
    }
  }
)
</script>
