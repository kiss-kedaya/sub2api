<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <AdminPageHeader :title="t('admin.settings.title')" :description="t('admin.settings.description')" />
      <!-- Loading State -->
      <div v-if="loading" class="flex items-center justify-center py-12">
        <UIcon name="i-lucide-loader-circle" class="size-8 animate-spin text-muted" />
      </div>

      <!-- Settings Form -->
      <UForm v-else :state="form" :disabled="saving" class="space-y-6" novalidate @submit="saveSettings">
        <!-- Toolbar: tab bar on the left, the form save action on the right (template settings toolbar) -->
        <div class="settings-toolbar relative z-10 flex min-h-14 flex-wrap items-center gap-3 border-b border-default bg-(--ui-bg) px-1 py-1">
          <UTabs
            :model-value="activeTab"
            :items="settingsTabItems"
            :content="false"
            variant="link"
            size="sm"
            class="settings-toolbar-tabs min-w-0 flex-1 basis-[40rem]"
            :ui="{
              list: 'overflow-x-auto overflow-y-hidden border-b-0 p-0 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden',
              trigger: 'shrink-0 py-3',
              indicator: 'bottom-0',
            }"
            @update:model-value="selectSettingsTab($event as SettingsTab)"
          >
            <template #default="{ item }">
              <span :id="`settings-tab-${item.value}`">{{ item.label }}</span>
            </template>
          </UTabs>
          <UButton
            v-show="activeTab !== 'backup' && activeTab !== 'appearance'"
            type="submit"
            :loading="saving"
            :disabled="saving || loadFailed"
            class="settings-toolbar-save ml-auto shrink-0"
          >
            {{ saving ? t("admin.settings.saving") : t("admin.settings.saveSettings") }}
          </UButton>
        </div>

        <div class="mx-auto flex w-full max-w-3xl flex-col gap-4 sm:gap-6 lg:gap-12">
        <!-- Tab: Security — Admin API Key -->
        <div v-show="activeTab === 'security'" class="flex flex-col gap-4 sm:gap-6 lg:gap-12">
          <GatewayIPNoticeSettings />
          <!-- Admin API Key Settings -->
          <SettingsSection
            id="settings-admin-api-key"
            :title="t('admin.settings.adminApiKey.title')"
            :description="t('admin.settings.adminApiKey.description')"
          >
            <!-- Security Warning -->
            <UAlert
              color="warning"
              variant="subtle"
              icon="i-lucide-triangle-alert"
              :description="t('admin.settings.adminApiKey.securityWarning')"
            />
            <!-- Loading State -->
            <div v-if="adminApiKeyLoading" class="flex items-center gap-2 text-muted">
              <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin text-muted" />
              {{ t("common.loading") }}
            </div>
            <!-- No Key Configured -->
            <div v-else-if="!adminApiKeyExists" class="flex items-center justify-between">
              <span class="text-muted">
                {{ t("admin.settings.adminApiKey.notConfigured") }}
              </span>
              <UButton
                :loading="adminApiKeyOperating"
                @click="createAdminApiKey"
                :disabled="adminApiKeyOperating"
                size="sm"
              >
                {{
                  adminApiKeyOperating
                    ? t("admin.settings.adminApiKey.creating")
                    : t("admin.settings.adminApiKey.create")
                }}
              </UButton>
            </div>
            <!-- Key Exists -->
            <template v-else>
              <div class="flex items-center justify-between">
                <UFormField :label="t('admin.settings.adminApiKey.currentKey')">
                  <code class="rounded-sm bg-elevated px-2 py-1 font-mono text-sm text-highlighted">
                    {{ adminApiKeyMasked }}
                  </code>
                </UFormField>
                <div class="flex gap-2">
                  <UButton
                    @click="regenerateAdminApiKey"
                    :disabled="adminApiKeyOperating"
                    color="neutral"
                    variant="outline"
                    size="sm"
                  >
                    {{
                      adminApiKeyOperating
                        ? t("admin.settings.adminApiKey.regenerating")
                        : t("admin.settings.adminApiKey.regenerate")
                    }}
                  </UButton>
                  <UButton
                    @click="deleteAdminApiKey"
                    :disabled="adminApiKeyOperating"
                    color="error"
                    variant="soft"
                    size="sm"
                  >
                    {{ t("admin.settings.adminApiKey.delete") }}
                  </UButton>
                </div>
              </div>
              <!-- Newly Generated Key Display -->
              <template v-if="newAdminApiKey">
                <p class="text-sm font-medium text-success">
                  {{ t("admin.settings.adminApiKey.keyWarning") }}
                </p>
                <div class="flex items-center gap-2">
                  <code
                    class="flex-1 select-all break-all rounded-sm border border-success/30 bg-default px-3 py-2 font-mono text-sm"
                  >
                    {{ newAdminApiKey }}
                  </code>
                  <UButton @click="copyNewKey" size="sm">
                    {{ t("admin.settings.adminApiKey.copyKey") }}
                  </UButton>
                </div>
                <p class="text-xs text-success">
                  {{ t("admin.settings.adminApiKey.usage") }}
                </p>
              </template>
            </template>
          </SettingsSection>
        </div>
        <!-- /Tab: Security — Admin API Key -->

        <!-- Tab: Gateway -->
        <div v-show="activeTab === 'gateway'" class="flex flex-col gap-4 sm:gap-6 lg:gap-12">
          <!-- Overload Cooldown (529) Settings -->
          <SettingsSection
            :title="t('admin.settings.overloadCooldown.title')"
            :description="t('admin.settings.overloadCooldown.description')"
            :loading="overloadCooldownLoading"
          >
            <template #actions>
              <UButton
                :loading="overloadCooldownSaving"
                @click="saveOverloadCooldownSettings"
                :disabled="overloadCooldownSaving"
              >
                {{ overloadCooldownSaving ? t("common.saving") : t("common.save") }}
              </UButton>
            </template>
            <UFormField
              :label="t('admin.settings.overloadCooldown.enabled')"
              :description="t('admin.settings.overloadCooldown.enabledHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="overloadCooldownForm.enabled" />
            </UFormField>
            <template v-if="overloadCooldownForm.enabled">
              <UFormField
                :label="t('admin.settings.overloadCooldown.cooldownMinutes')"
                :description="t('admin.settings.overloadCooldown.cooldownMinutesHint')"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <UInput
                  v-model.number="overloadCooldownForm.cooldown_minutes"
                  type="number"
                  min="1"
                  max="120"
                  class="w-32"
                />
              </UFormField>
            </template>
          </SettingsSection>

          <!-- Rate Limit Cooldown (429) Settings -->
          <SettingsSection
            :title="t('admin.settings.rateLimit429Cooldown.title')"
            :description="t('admin.settings.rateLimit429Cooldown.description')"
            :loading="rateLimit429CooldownLoading"
          >
            <template #actions>
              <UButton
                :loading="rateLimit429CooldownSaving"
                @click="saveRateLimit429CooldownSettings"
                :disabled="rateLimit429CooldownSaving"
              >
                {{ rateLimit429CooldownSaving ? t("common.saving") : t("common.save") }}
              </UButton>
            </template>
            <UFormField
              :label="t('admin.settings.rateLimit429Cooldown.enabled')"
              :description="t('admin.settings.rateLimit429Cooldown.enabledHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="rateLimit429CooldownForm.enabled" />
            </UFormField>
            <template v-if="rateLimit429CooldownForm.enabled">
              <UFormField
                :label="t(
                      'admin.settings.rateLimit429Cooldown.cooldownSeconds',
                    )"
                :description="t(
                      'admin.settings.rateLimit429Cooldown.cooldownSecondsHint',
                    )"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <UInput
                  v-model.number="rateLimit429CooldownForm.cooldown_seconds"
                  type="number"
                  min="1"
                  max="7200"
                  class="w-32"
                />
              </UFormField>
            </template>
          </SettingsSection>

          <!-- Stream Timeout Settings -->
          <SettingsSection
            :title="t('admin.settings.streamTimeout.title')"
            :description="t('admin.settings.streamTimeout.description')"
            :loading="streamTimeoutLoading"
          >
            <template #actions>
              <UButton
                :loading="streamTimeoutSaving"
                @click="saveStreamTimeoutSettings"
                :disabled="streamTimeoutSaving"
              >
                {{ streamTimeoutSaving ? t("common.saving") : t("common.save") }}
              </UButton>
            </template>
            <!-- Loading State -->
            <!-- Enable Stream Timeout -->
            <UFormField
              :label="t('admin.settings.streamTimeout.enabled')"
              :description="t('admin.settings.streamTimeout.enabledHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="streamTimeoutForm.enabled" />
            </UFormField>
            <!-- Settings - Only show when enabled -->
            <template v-if="streamTimeoutForm.enabled">
              <!-- Action -->
              <UFormField
                :label="t('admin.settings.streamTimeout.action')"
                :description="t('admin.settings.streamTimeout.actionHint')"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <Select
                  v-model="streamTimeoutForm.action"
                  :options="[{ value: 'temp_unsched', label: t('admin.settings.streamTimeout.actionTempUnsched') }, { value: 'error', label: t('admin.settings.streamTimeout.actionError') }, { value: 'none', label: t('admin.settings.streamTimeout.actionNone') }]"
                  class="w-64"
                />
              </UFormField>
              <!-- Temp Unsched Minutes (only show when action is temp_unsched) -->
              <UFormField
                v-if="streamTimeoutForm.action === 'temp_unsched'"
                :label="t('admin.settings.streamTimeout.tempUnschedMinutes')"
                :description="t('admin.settings.streamTimeout.tempUnschedMinutesHint')"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <UInput
                  v-model.number="streamTimeoutForm.temp_unsched_minutes"
                  type="number"
                  min="1"
                  max="60"
                  class="w-32"
                />
              </UFormField>
              <!-- Threshold Count -->
              <UFormField
                :label="t('admin.settings.streamTimeout.thresholdCount')"
                :description="t('admin.settings.streamTimeout.thresholdCountHint')"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <UInput
                  v-model.number="streamTimeoutForm.threshold_count"
                  type="number"
                  min="1"
                  max="10"
                  class="w-32"
                />
              </UFormField>
              <!-- Threshold Window Minutes -->
              <UFormField
                :label="t('admin.settings.streamTimeout.thresholdWindowMinutes')"
                :description="t(
                      'admin.settings.streamTimeout.thresholdWindowMinutesHint',
                    )"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <UInput
                  v-model.number="
                    streamTimeoutForm.threshold_window_minutes
                  "
                  type="number"
                  min="1"
                  max="60"
                  class="w-32"
                />
              </UFormField>
            </template>
            <!-- Save Button -->
          </SettingsSection>

          <!-- Request Rectifier Settings -->
          <SettingsSection
            :title="t('admin.settings.rectifier.title')"
            :description="t('admin.settings.rectifier.description')"
            :loading="rectifierLoading"
          >
            <template #actions>
              <UButton
                :loading="rectifierSaving"
                @click="saveRectifierSettings"
                :disabled="rectifierSaving"
              >
                {{ rectifierSaving ? t("common.saving") : t("common.save") }}
              </UButton>
            </template>
            <!-- Loading State -->
            <!-- Master Toggle -->
            <UFormField
              :label="t('admin.settings.rectifier.enabled')"
              :description="t('admin.settings.rectifier.enabledHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="rectifierForm.enabled" />
            </UFormField>
            <!-- Sub-toggles (only show when master is enabled) -->
            <template v-if="rectifierForm.enabled">
              <!-- Thinking Signature Rectifier -->
              <UFormField
                :label="t('admin.settings.rectifier.thinkingSignature')"
                :description="t('admin.settings.rectifier.thinkingSignatureHint')"
                class="flex items-center justify-between gap-4"
              >
                <Toggle v-model="rectifierForm.thinking_signature_enabled" />
              </UFormField>
              <!-- Thinking Budget Rectifier -->
              <UFormField
                :label="t('admin.settings.rectifier.thinkingBudget')"
                :description="t('admin.settings.rectifier.thinkingBudgetHint')"
                class="flex items-center justify-between gap-4"
              >
                <Toggle v-model="rectifierForm.thinking_budget_enabled" />
              </UFormField>
              <!-- API Key Signature Rectifier -->
              <UFormField
                :label="t('admin.settings.rectifier.apikeySignature')"
                :description="t('admin.settings.rectifier.apikeySignatureHint')"
                class="flex items-center justify-between gap-4"
              >
                <Toggle v-model="rectifierForm.apikey_signature_enabled" />
              </UFormField>
              <!-- Custom Patterns (only when apikey_signature_enabled) -->
              <UFormField
                v-if="rectifierForm.apikey_signature_enabled"
                :label="t('admin.settings.rectifier.apikeyPatterns')"
                :description="t('admin.settings.rectifier.apikeyPatternsHint')"
                :ui="{ container: 'w-full mt-3' }"
              >
                <div
                  v-for="(
                    _, index
                  ) in rectifierForm.apikey_signature_patterns"
                  :key="index"
                  class="flex items-center gap-2"
                >
                  <UInput
                    v-model="rectifierForm.apikey_signature_patterns[index]"
                    type="text"
                    :placeholder="
                      t('admin.settings.rectifier.apikeyPatternPlaceholder')
                    "
                    size="sm"
                    class="flex-1"
                  />
                  <UButton
                    icon="i-lucide-x"
                    square
                    @click="
                      rectifierForm.apikey_signature_patterns.splice(
                        index,
                        1,
                      )
                    "
                    color="error"
                    variant="soft"
                    size="xs"
                  />
                </div>
                <UButton
                  @click="rectifierForm.apikey_signature_patterns.push('')"
                  color="neutral"
                  variant="outline"
                  size="xs"
                >
                  + {{ t("admin.settings.rectifier.addPattern") }}
                </UButton>
              </UFormField>
            </template>
            <!-- Save Button -->
          </SettingsSection>
          <!-- Beta Policy Settings -->
          <SettingsSection
            :title="t('admin.settings.betaPolicy.title')"
            :description="t('admin.settings.betaPolicy.description')"
            :loading="betaPolicyLoading"
          >
            <template #actions>
              <UButton
                :loading="betaPolicySaving"
                @click="saveBetaPolicySettings"
                :disabled="betaPolicySaving"
              >
                {{ betaPolicySaving ? t("common.saving") : t("common.save") }}
              </UButton>
            </template>
            <!-- Loading State -->
            <!-- Rule Cards -->
            <div
              v-for="rule in betaPolicyForm.rules"
              :key="rule.beta_token"
              class="border-t border-default pt-4 first:border-t-0 first:pt-0"
            >
              <div class="mb-3 flex items-center gap-2">
                <span class="text-sm font-medium text-highlighted">
                  {{ getBetaDisplayName(rule.beta_token) }}
                </span>
                <span class="rounded-sm bg-elevated px-2 py-0.5 text-xs text-muted">
                  {{ rule.beta_token }}
                </span>
              </div>

              <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <!-- Action -->
                <UFormField :label="t('admin.settings.betaPolicy.action')">
                  <Select
                    :modelValue="rule.action"
                    @update:modelValue="rule.action = $event as any"
                    :options="betaPolicyActionOptions"
                  />
                </UFormField>

                <!-- Scope -->
                <UFormField :label="t('admin.settings.betaPolicy.scope')">
                  <Select
                    :modelValue="rule.scope"
                    @update:modelValue="rule.scope = $event as any"
                    :options="betaPolicyScopeOptions"
                  />
                </UFormField>
              </div>

              <!-- Error Message (only when action=block) -->
              <UFormField
                v-if="rule.action === 'block'"
                :label="t('admin.settings.betaPolicy.errorMessage')"
                :description="t('admin.settings.betaPolicy.errorMessageHint')"
              >
                <UInput
                  v-model="rule.error_message"
                  type="text"
                  :placeholder="
                    t('admin.settings.betaPolicy.errorMessagePlaceholder')
                  "
                  class="w-full"
                />
              </UFormField>

              <!-- Quick Presets (only for tokens with presets) -->
              <UFormField
                v-if="betaPresets[rule.beta_token]?.length"
                :label="t('admin.settings.betaPolicy.quickPresets')"
              >
                <div class="flex flex-wrap gap-2">
                  <UButton
                    v-for="preset in betaPresets[rule.beta_token]"
                    :key="preset.label"
                    @click="applyBetaPreset(rule, preset)"
                    :title="preset.description"
                    size="sm"
                  >
                    {{ preset.label }}
                  </UButton>
                </div>
              </UFormField>

              <!-- Model Whitelist -->
              <UFormField
                :label="t('admin.settings.betaPolicy.modelWhitelist')"
                :description="t('admin.settings.betaPolicy.modelWhitelistHint')"
              >
                <div
                  v-for="(_, index) in rule.model_whitelist || []"
                  :key="index"
                  class="mb-1.5 flex items-center gap-2"
                >
                  <UInput
                    v-model="rule.model_whitelist![index]"
                    type="text"
                    :placeholder="
                      t('admin.settings.betaPolicy.modelPatternPlaceholder')
                    "
                    size="sm"
                    class="flex-1"
                  />
                  <UButton
                    icon="i-lucide-x"
                    square
                    @click="rule.model_whitelist!.splice(index, 1)"
                    color="error"
                    variant="ghost"
                    class="shrink-0"
                  />
                </div>
                <UButton
                  icon="i-lucide-plus"
                  @click="
                    if (!rule.model_whitelist) rule.model_whitelist = [];
                    rule.model_whitelist.push('');
                  "
                  variant="link"
                  size="sm"
                  class="mb-2"
                >
                  {{ t("admin.settings.betaPolicy.addModelPattern") }}
                </UButton>
                <div class="flex flex-wrap items-center gap-1.5">
                  <span class="text-xs text-dimmed">{{ t("admin.settings.betaPolicy.commonPatterns") }}:</span>
                  <UButton
                    v-for="pattern in commonModelPatterns"
                    :key="pattern"
                    @click="addQuickPattern(rule, pattern)"
                    size="sm"
                  >
                    {{ pattern }}
                  </UButton>
                </div>
              </UFormField>

              <!-- Fallback Action (only when model_whitelist is non-empty) -->
              <UFormField
                v-if="
                  rule.model_whitelist && rule.model_whitelist.length > 0
                "
                :label="t('admin.settings.betaPolicy.fallbackAction')"
                :description="t('admin.settings.betaPolicy.fallbackActionHint')"
              >
                <Select
                  :modelValue="rule.fallback_action || 'pass'"
                  @update:modelValue="rule.fallback_action = $event as any"
                  :options="betaPolicyActionOptions"
                />
                <div v-if="rule.fallback_action === 'block'" class="mt-2">
                  <UInput
                    v-model="rule.fallback_error_message"
                    type="text"
                    :placeholder="
                      t(
                        'admin.settings.betaPolicy.fallbackErrorMessagePlaceholder',
                      )
                    "
                    class="w-full"
                  />
                  <p class="mt-1 text-xs text-dimmed">
                    {{ t("admin.settings.betaPolicy.errorMessageHint") }}
                  </p>
                </div>
              </UFormField>
            </div>
            <!-- Save Button -->
          </SettingsSection>
          <!-- OpenAI Fast/Flex Policy Settings -->
          <SettingsSection
            :title="t('admin.settings.openaiFastPolicy.title')"
            :description="t('admin.settings.openaiFastPolicy.description')"
          >
            <!-- Empty state -->
            <div
              v-if="openaiFastPolicyForm.rules.length === 0"
              class="py-2 text-center text-sm text-muted"
            >
              {{ t("admin.settings.openaiFastPolicy.empty") }}
            </div>
            <!-- Rule Cards -->
            <div
              v-for="(rule, ruleIndex) in openaiFastPolicyForm.rules"
              :key="ruleIndex"
              class="border-t border-default pt-4 first:border-t-0 first:pt-0"
            >
              <div class="mb-3 flex items-center justify-between">
                <span class="text-sm font-medium text-highlighted">
                  {{ t("admin.settings.openaiFastPolicy.ruleHeader", { index: ruleIndex + 1, }) }}
                </span>
                <UButton
                  icon="i-lucide-x"
                  square
                  :aria-label="t('admin.settings.openaiFastPolicy.removeRule')"
                  @click="removeOpenAIFastPolicyRule(ruleIndex)"
                  :title="t('admin.settings.openaiFastPolicy.removeRule')"
                  color="error"
                  variant="ghost"
                />
              </div>

              <div
                class="mb-4 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted"
                :data-testid="`openai-fast-policy-summary-${ruleIndex}`"
              >
                <span class="font-medium text-toned">
                  {{
                    t(
                      hasOpenAIFastPolicyTargetModels(rule)
                        ? "admin.settings.openaiFastPolicy.summaryTargetModels"
                        : "admin.settings.openaiFastPolicy.summaryAllModels",
                    )
                  }}
                </span>
                <span aria-hidden="true">→</span>
                <span class="inline-flex items-center rounded-sm bg-primary/10 px-2 py-0.5 font-medium text-primary">
                  {{ openaiFastPolicyActionSummary(rule.action) }}
                </span>
                <template v-if="hasOpenAIFastPolicyTargetModels(rule)">
                  <span aria-hidden="true">·</span>
                  <span class="font-medium text-toned">
                    {{ t( "admin.settings.openaiFastPolicy.summaryOtherModels", ) }}
                  </span>
                  <span aria-hidden="true">→</span>
                  <span class="inline-flex items-center rounded-sm bg-elevated px-2 py-0.5 font-medium text-toned">
                    {{ openaiFastPolicyActionSummary( rule.fallback_action || "pass", ) }}
                  </span>
                </template>
              </div>

              <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
                <!-- Service Tier -->
                <UFormField :label="t('admin.settings.openaiFastPolicy.serviceTier')">
                  <Select
                    :modelValue="rule.service_tier"
                    @update:modelValue="
                      rule.service_tier = $event as
                        | 'all'
                        | 'priority'
                        | 'flex'
                        | 'missing'
                    "
                    :options="openaiFastPolicyTierOptions"
                  />
                </UFormField>

                <!-- Action -->
                <UFormField :label="t('admin.settings.openaiFastPolicy.action')">
                  <Select
                    :modelValue="rule.action"
                    @update:modelValue="
                      rule.action = $event as
                        | 'pass'
                        | 'filter'
                        | 'block'
                        | 'force_priority'
                    "
                    :options="openaiFastPolicyActionOptions"
                  />
                </UFormField>

                <!-- Scope -->
                <UFormField :label="t('admin.settings.openaiFastPolicy.scope')">
                  <Select
                    :modelValue="rule.scope"
                    @update:modelValue="
                      rule.scope = $event as
                        | 'all'
                        | 'oauth'
                        | 'apikey'
                        | 'bedrock'
                    "
                    :options="openaiFastPolicyScopeOptions"
                  />
                </UFormField>
              </div>

              <!-- User Scope -->
              <UFormField
                :label="t('admin.settings.openaiFastPolicy.userIds')"
                :description="t('admin.settings.openaiFastPolicy.userIdsHint')"
              >
                <OpenAIFastPolicyUserSelector
                  :model-value="rule.user_ids || []"
                  @update:model-value="rule.user_ids = $event"
                />
              </UFormField>

              <!-- Error Message (only when action=block) -->
              <UFormField
                v-if="rule.action === 'block'"
                :label="t('admin.settings.openaiFastPolicy.errorMessage')"
                :description="t('admin.settings.openaiFastPolicy.errorMessageHint')"
              >
                <UInput
                  v-model="rule.error_message"
                  type="text"
                  :placeholder="
                    t(
                      'admin.settings.openaiFastPolicy.errorMessagePlaceholder',
                    )
                  "
                  class="w-full"
                />
              </UFormField>

              <!-- Target Models -->
              <UFormField
                role="group"
                :aria-labelledby="`openai-fast-policy-models-label-${ruleIndex}`"
                :aria-describedby="`openai-fast-policy-models-hint-${ruleIndex}`"
              >
                <template #label>
                  <span :id="`openai-fast-policy-models-label-${ruleIndex}`">{{ t("admin.settings.openaiFastPolicy.modelWhitelist") }}</span>
                </template>
                <template #description>
                  <span :id="`openai-fast-policy-models-hint-${ruleIndex}`">{{ t("admin.settings.openaiFastPolicy.modelWhitelistHint") }}</span>
                </template>
                <div
                  v-for="(_, patternIdx) in rule.model_whitelist || []"
                  :key="patternIdx"
                  class="mb-1.5 flex items-center gap-2"
                >
                  <UInput
                    v-model="rule.model_whitelist![patternIdx]"
                    type="text"
                    :placeholder="
                      t(
                        'admin.settings.openaiFastPolicy.modelPatternPlaceholder',
                      )
                    "
                    size="sm"
                    class="flex-1"
                  />
                  <UButton
                    icon="i-lucide-x"
                    square
                    @click="
                      removeOpenAIFastPolicyModelPattern(rule, patternIdx)
                    "
                    color="error"
                    variant="ghost"
                    class="shrink-0"
                  />
                </div>
                <UButton
                  icon="i-lucide-plus"
                  @click="addOpenAIFastPolicyModelPattern(rule)"
                  variant="link"
                  size="sm"
                  class="mb-2"
                >
                  {{ t("admin.settings.openaiFastPolicy.addModelPattern") }}
                </UButton>
              </UFormField>

              <!-- Other Models Action (only when target models are non-empty) -->
              <UFormField
                v-if="hasOpenAIFastPolicyTargetModels(rule)"
                :label="t('admin.settings.openaiFastPolicy.fallbackAction')"
                :description="t('admin.settings.openaiFastPolicy.fallbackActionHint')"
              >
                <Select
                  :modelValue="rule.fallback_action || 'pass'"
                  @update:modelValue="
                    rule.fallback_action = $event as
                      | 'pass'
                      | 'filter'
                      | 'block'
                      | 'force_priority'
                  "
                  :options="openaiFastPolicyActionOptions"
                />
                <div v-if="rule.fallback_action === 'block'" class="mt-2">
                  <UInput
                    v-model="rule.fallback_error_message"
                    type="text"
                    :placeholder="
                      t(
                        'admin.settings.openaiFastPolicy.fallbackErrorMessagePlaceholder',
                      )
                    "
                    class="w-full"
                  />
                </div>
              </UFormField>
            </div>
            <!-- Add Rule Button -->
            <UButton
              icon="i-lucide-plus"
              @click="addOpenAIFastPolicyRule"
              color="neutral"
              variant="outline"
              size="sm"
            >
              {{ t("admin.settings.openaiFastPolicy.addRule") }}
            </UButton>
            <p class="text-xs text-dimmed">
              {{ t("admin.settings.openaiFastPolicy.saveHint") }}
            </p>
          </SettingsSection>
        </div>
        <!-- /Tab: Gateway -->

        <!-- Tab: Security — Registration, Turnstile, LinuxDo -->
        <div v-show="activeTab === 'security'" class="flex flex-col gap-4 sm:gap-6 lg:gap-12">
          <!-- Registration Settings -->
          <SettingsSection
            id="settings-registration"
            :title="t('admin.settings.registration.title')"
            :description="t('admin.settings.registration.description')"
          >
            <!-- Enable Registration -->
            <UFormField
              :label="t('admin.settings.registration.enableRegistration')"
              :description="t('admin.settings.registration.enableRegistrationHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.registration_enabled" />
            </UFormField>
            <!-- Email Verification -->
            <UFormField
              :label="t('admin.settings.registration.emailVerification')"
              :description="t('admin.settings.registration.emailVerificationHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.email_verify_enabled" />
            </UFormField>
            <!-- Email Suffix Whitelist -->
            <UFormField
              :label="t('admin.settings.registration.emailSuffixWhitelist')"
              :ui="{ container: 'w-full mt-3' }"
            >
              <template #description>
                <span class="block">
                  {{ t("admin.settings.registration.emailSuffixWhitelistHint") }}
                </span>
                <span class="block mt-1">
                  {{ t( "admin.settings.registration.emailSuffixWhitelistInputHint", ) }}
                </span>
              </template>
              <div class="mt-3 rounded-lg border border-default bg-default p-2">
                <div class="flex flex-wrap items-center gap-2">
                  <span
                    v-for="suffix in registrationEmailSuffixWhitelistTags"
                    :key="suffix"
                    class="inline-flex items-center gap-1 rounded-sm bg-elevated px-2 py-1 text-xs font-mono text-toned"
                  >
                    <span>{{ suffix }}</span>
                    <UButton
                      icon="i-lucide-x"
                      square
                      @click="
                        removeRegistrationEmailSuffixWhitelistTag(suffix)
                      "
                      color="neutral"
                      variant="ghost"
                    />
                  </span>

                  <div
                    class="flex min-w-[220px] flex-1 items-center gap-1 rounded-sm border border-transparent px-2 py-1 focus-within:border-primary-300"
                  >
                    <UInput
                      v-model="registrationEmailSuffixWhitelistDraft"
                      type="text"
                      :placeholder="
                        t(
                          'admin.settings.registration.emailSuffixWhitelistPlaceholder',
                        )
                      "
                      @input="
                        handleRegistrationEmailSuffixWhitelistDraftInput
                      "
                      @keydown="
                        handleRegistrationEmailSuffixWhitelistDraftKeydown
                      "
                      @blur="commitRegistrationEmailSuffixWhitelistDraft"
                      @paste="handleRegistrationEmailSuffixWhitelistPaste"
                      class="w-full font-mono"
                    />
                  </div>
                </div>
              </div>
            </UFormField>
            <!-- Email Domain Quota -->
            <UFormField
              :label="t('admin.settings.registration.emailDomainQuota')"
              :description="t('admin.settings.registration.emailDomainQuotaHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.registration_email_domain_quota_enabled" />
            </UFormField>
            <UFormField
              :label="t('admin.settings.registration.emailAliasBlock')"
              :description="t('admin.settings.registration.emailAliasBlockHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.registration_email_alias_block_enabled" />
            </UFormField>
            <!-- Promo Code -->
            <UFormField
              :label="t('admin.settings.registration.promoCode')"
              :description="t('admin.settings.registration.promoCodeHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.promo_code_enabled" />
            </UFormField>
            <!-- Invitation Code -->
            <UFormField
              :label="t('admin.settings.registration.invitationCode')"
              :description="t('admin.settings.registration.invitationCodeHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.invitation_code_enabled" />
            </UFormField>
            <!-- Password Reset - Only show when email verification is enabled -->
            <UFormField
              v-if="form.email_verify_enabled"
              :label="t('admin.settings.registration.passwordReset')"
              :description="t('admin.settings.registration.passwordResetHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.password_reset_enabled" />
            </UFormField>
            <!-- Frontend URL - Only show when password reset is enabled -->
            <UFormField
              v-if="form.email_verify_enabled && form.password_reset_enabled"
              :label="t('admin.settings.registration.frontendUrl')"
              :description="t('admin.settings.registration.frontendUrlHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
              :ui="{ container: 'w-full sm:max-w-sm' }"
            >
              <UInput
                v-model="form.frontend_url"
                type="url"
                :placeholder="
                  t('admin.settings.registration.frontendUrlPlaceholder')
                "
                class="w-full"
              />
            </UFormField>
            <!-- TOTP 2FA -->
            <UFormField
              :label="t('admin.settings.registration.totp')"
              :description="t('admin.settings.registration.totpHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.totp_enabled" :disabled="!form.totp_encryption_key_configured" />
              <p v-if="!form.totp_encryption_key_configured" class="text-sm text-warning mt-2">
                {{ t("admin.settings.registration.totpKeyNotConfigured") }}
              </p>
            </UFormField>
            <!-- Passkey sign-in -->
            <div class="border-default" data-testid="passkey-settings">
              <UFormField
                :label="t('admin.settings.security.passkey')"
                :description="t('admin.settings.security.passkeyHint')"
                class="flex items-center justify-between gap-4"
              >
                <Toggle
                  v-model="form.passkey_enabled"
                  data-testid="passkey-toggle"
                  :disabled="!form.passkey_configured"
                />
              </UFormField>
              <div
                class="mt-3 rounded-lg border px-3 py-2 text-sm"
                :class="
                  form.passkey_configured
                    ? 'border-success/30 bg-success/10 text-success'
                    : 'border-warning/30 bg-warning/10 text-warning'
                "
                data-testid="passkey-config-status"
              >
                <p class="font-medium">
                  {{
                    form.passkey_configured
                      ? t("admin.settings.security.passkeyConfigured")
                      : t("admin.settings.security.passkeyNotConfigured")
                  }}
                </p>
                <p class="mt-1 break-all">
                  {{ t("admin.settings.security.passkeyRPID") }}:
                  {{ form.passkey_rp_id || t("admin.settings.security.passkeyValueNotConfigured") }}
                </p>
                <p class="mt-1 break-all">
                  {{ t("admin.settings.security.passkeyOrigins") }}:
                  {{
                    form.passkey_rp_origins.length > 0
                      ? form.passkey_rp_origins.join(", ")
                      : t(
                          "admin.settings.security.passkeyValueNotConfigured",
                        )
                  }}
                </p>
                <p v-if="!form.passkey_configured" class="mt-2">
                  {{ t("admin.settings.security.passkeyDeploymentHint") }}
                </p>
              </div>
            </div>
            <!-- 敏感操作 step-up 2FA -->
            <UFormField
              :label="t('admin.settings.security.stepUp')"
              :description="t('admin.settings.security.stepUpHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.step_up_enabled" />
            </UFormField>
            <!-- 会话 IP/UA 绑定 -->
            <UFormField
              :label="t('admin.settings.security.sessionBinding')"
              :description="t('admin.settings.security.sessionBindingHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.session_binding_enabled" />
            </UFormField>
            <!-- 审计日志保留天数 -->
            <UFormField
              :label="t('admin.settings.security.auditRetention')"
              :description="t('admin.settings.security.auditRetentionHint')"
              class="flex max-sm:flex-col justify-between sm:items-center gap-4"
            >
              <UInput v-model.number="form.audit_log_retention_days" type="number" min="0" class="w-28 text-right" />
            </UFormField>
          </SettingsSection>

          <!-- API Key IP ACL Settings -->
          <SettingsSection
            :title="t('admin.settings.apiKeyAcl.title')"
            :description="t('admin.settings.apiKeyAcl.description')"
          >
            <UFormField
              :label="t('admin.settings.apiKeyAcl.trustForwardedIp')"
              :description="t('admin.settings.apiKeyAcl.trustForwardedIpHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.api_key_acl_trust_forwarded_ip" />
            </UFormField>
            <UFormField
              v-if="form.api_key_acl_trust_forwarded_ip"
              :label="t('admin.settings.apiKeyAcl.forwardedClientIpHeaders')"
              :ui="{ container: 'w-full mt-3' }"
            >
              <template #description>
                <span class="block">
                  {{ t("admin.settings.apiKeyAcl.forwardedClientIpHeadersHint") }}
                </span>
                <span class="block mt-1">
                  {{ t("admin.settings.apiKeyAcl.forwardedClientIpHeadersRiskHint") }}
                </span>
              </template>
              <div class="mt-3 rounded-lg border border-default bg-default p-2">
                <div class="flex flex-wrap items-center gap-2">
                  <span
                    v-for="header in form.forwarded_client_ip_headers"
                    :key="header"
                    data-testid="forwarded-client-ip-header-tag"
                    class="inline-flex items-center gap-1 rounded-sm bg-elevated px-2 py-1 text-xs font-mono text-toned"
                  >
                    <span>{{ header }}</span>
                    <UButton
                      icon="i-lucide-x"
                      square
                      :aria-label="t('admin.settings.apiKeyAcl.removeForwardedClientIpHeader', { header })"
                      @click="removeForwardedClientIpHeader(header)"
                      color="neutral"
                      variant="ghost"
                    />
                  </span>
                  <div
                    class="flex min-w-[220px] flex-1 items-center gap-1 rounded-sm border border-transparent px-2 py-1 focus-within:border-primary-300"
                  >
                    <UInput
                      id="forwarded-client-ip-headers"
                      v-model="forwardedClientIpHeaderDraft"
                      data-testid="forwarded-client-ip-headers-input"
                      type="text"
                      :placeholder="t('admin.settings.apiKeyAcl.forwardedClientIpHeadersPlaceholder')"
                      @keydown="handleForwardedClientIpHeaderKeydown"
                      @blur="commitForwardedClientIpHeaderDraft"
                      @paste="handleForwardedClientIpHeaderPaste"
                      class="w-full font-mono"
                    />
                  </div>
                </div>
              </div>
            </UFormField>
          </SettingsSection>

          <!-- Panel API Rate Limit Settings -->
          <SettingsSection
            :title="t('admin.settings.panelRateLimit.title')"
            :description="t('admin.settings.panelRateLimit.description')"
            :loading="panelRateLimitLoading"
          >
            <template #actions>
              <UButton
                :loading="panelRateLimitSaving"
                data-testid="panel-rate-limit-save"
                @click="savePanelRateLimitSettings"
                :disabled="panelRateLimitSaving"
              >
                {{ panelRateLimitSaving ? t("common.saving") : t("common.save") }}
              </UButton>
            </template>
            <!-- 计数维度说明：按账号计数，反代部署无误伤 -->
            <UAlert
              color="info"
              variant="subtle"
              icon="i-lucide-info"
              :description="t('admin.settings.panelRateLimit.proxySafeNote')"
            />
            <UFormField
              :label="t('admin.settings.panelRateLimit.enabled')"
              :description="t('admin.settings.panelRateLimit.enabledHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="panelRateLimitForm.enabled" />
            </UFormField>
            <template v-if="panelRateLimitForm.enabled">
              <div class="grid grid-cols-1 gap-6 sm:grid-cols-2">
                <UFormField
                  :label="t('admin.settings.panelRateLimit.userRpm')"
                  :description="t('admin.settings.panelRateLimit.userRpmHint')"
                >
                  <div class="flex items-center gap-2">
                    <UInput
                      v-model.number="panelRateLimitForm.user_rpm"
                      data-testid="panel-rate-limit-user-rpm"
                      type="number"
                      min="0"
                      max="100000"
                      class="w-32"
                    />
                    <span class="text-sm text-muted">
                      {{ t("admin.settings.panelRateLimit.perMinute") }}
                    </span>
                  </div>
                </UFormField>

                <UFormField
                  :label="t('admin.settings.panelRateLimit.heavyRpm')"
                  :description="t('admin.settings.panelRateLimit.heavyRpmHint')"
                >
                  <div class="flex items-center gap-2">
                    <UInput
                      v-model.number="panelRateLimitForm.heavy_rpm"
                      type="number"
                      min="0"
                      max="100000"
                      class="w-32"
                    />
                    <span class="text-sm text-muted">
                      {{ t("admin.settings.panelRateLimit.perMinute") }}
                    </span>
                  </div>
                </UFormField>

                <UFormField
                  :label="t('admin.settings.panelRateLimit.publicIpRpm')"
                  :description="t('admin.settings.panelRateLimit.publicIpRpmHint')"
                >
                  <div class="flex items-center gap-2">
                    <UInput
                      v-model.number="panelRateLimitForm.public_ip_rpm"
                      type="number"
                      min="0"
                      max="100000"
                      class="w-32"
                    />
                    <span class="text-sm text-muted">
                      {{ t("admin.settings.panelRateLimit.perMinute") }}
                    </span>
                  </div>
                </UFormField>
              </div>
              <UFormField
                :label="t('admin.settings.panelRateLimit.exemptAdmin')"
                :description="t('admin.settings.panelRateLimit.exemptAdminHint')"
                class="flex items-center justify-between gap-4"
              >
                <Toggle v-model="panelRateLimitForm.exempt_admin" />
              </UFormField>
            </template>
          </SettingsSection>

          <!-- 人机验证 Settings -->
          <SettingsSection
            id="settings-captcha"
            :title="t('admin.settings.captcha.title')"
            :description="t('admin.settings.captcha.description')"
          >
            <!-- Enable Captcha -->
            <UFormField
              :label="t('admin.settings.captcha.enable')"
              :description="t('admin.settings.captcha.enableHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="captchaMasterEnabled" data-testid="captcha-enabled-toggle" />
            </UFormField>
            <!-- Provider fields - Only show when enabled -->
            <template v-if="captchaMasterEnabled">
              <!-- Provider Selector -->
              <UFormField :label="t('admin.settings.captcha.provider')" :ui="{ container: 'w-full mt-3' }">
                <div class="grid grid-cols-3 gap-2 rounded-lg bg-elevated p-1">
                  <UButton
                    data-testid="captcha-provider-turnstile"
                    @click="selectCaptchaProvider('turnstile')"
                    color="neutral"
                    :variant="captchaProviderSelection === 'turnstile' ? 'outline' : 'ghost'"
                    class="justify-center"
                  >
                    {{ t("admin.settings.captcha.providerTurnstile") }}
                  </UButton>
                  <UButton
                    data-testid="captcha-provider-tencent"
                    @click="selectCaptchaProvider('tencent')"
                    color="neutral"
                    :variant="captchaProviderSelection === 'tencent' ? 'outline' : 'ghost'"
                    class="justify-center"
                  >
                    {{ t("admin.settings.captcha.providerTencent") }}
                  </UButton>
                  <UButton
                    data-testid="captcha-provider-aliyun"
                    @click="selectCaptchaProvider('aliyun')"
                    color="neutral"
                    :variant="captchaProviderSelection === 'aliyun' ? 'outline' : 'ghost'"
                    class="justify-center"
                  >
                    {{ t("admin.settings.captcha.providerAliyun") }}
                  </UButton>
                </div>
              </UFormField>
              <!-- Cloudflare Turnstile fields -->
              <div v-if="captchaProviderSelection === 'turnstile'" class="grid grid-cols-1 gap-6">
                <UFormField :label="t('admin.settings.turnstile.siteKey')">
                  <template #description>
                    <span>
                      {{ t("admin.settings.turnstile.siteKeyHint") }}
                      <a href="https://dash.cloudflare.com/" target="_blank" class="text-primary hover:underline">{{ t("admin.settings.turnstile.cloudflareDashboard") }}</a>
                    </span>
                  </template>
                  <UInput
                    v-model="form.turnstile_site_key"
                    type="text"
                    placeholder="0x4AAAAAAA..."
                    class="w-full font-mono"
                  />
                </UFormField>
                <UFormField
                  :label="t('admin.settings.turnstile.secretKey')"
                  :description="form.turnstile_secret_key_configured
                        ? t(
                            'admin.settings.turnstile.secretKeyConfiguredHint',
                          )
                        : t('admin.settings.turnstile.secretKeyHint')"
                >
                  <UInput
                    v-model="form.turnstile_secret_key"
                    type="password"
                    placeholder="0x4AAAAAAA..."
                    class="w-full font-mono"
                  />
                </UFormField>
              </div>
              <!-- Tencent Captcha fields -->
              <template v-else-if="captchaProviderSelection === 'tencent'">
                <UFormField
                  :label="t('admin.settings.tencentCaptcha.region')"
                  :description="t('admin.settings.tencentCaptcha.regionHint')"
                  :ui="{ container: 'w-full mt-3' }"
                >
                  <div class="grid grid-cols-2 gap-2 rounded-lg bg-elevated p-1">
                    <UButton
                      data-testid="tencent-captcha-region-cn"
                      @click="form.tencent_captcha_region = 'cn'"
                      :color="form.tencent_captcha_region !== 'intl' ? 'primary' : 'neutral'"
                      :variant="form.tencent_captcha_region !== 'intl' ? 'subtle' : 'outline'"
                      size="sm"
                      class="justify-center"
                    >
                      {{ t("admin.settings.tencentCaptcha.regionCn") }}
                    </UButton>
                    <UButton
                      data-testid="tencent-captcha-region-intl"
                      @click="form.tencent_captcha_region = 'intl'"
                      :color="form.tencent_captcha_region === 'intl' ? 'primary' : 'neutral'"
                      :variant="form.tencent_captcha_region === 'intl' ? 'subtle' : 'outline'"
                      size="sm"
                      class="justify-center"
                    >
                      {{ t("admin.settings.tencentCaptcha.regionIntl") }}
                    </UButton>
                  </div>
                </UFormField>
                <UFormField
                  :label="t('admin.settings.tencentCaptcha.appCredentialsTitle')"
                  :description="t('admin.settings.tencentCaptcha.appCredentialsHint')"
                  :ui="{ container: 'w-full mt-3' }"
                >
                  <UFormField :label="t('admin.settings.tencentCaptcha.appId')">
                    <UInput
                      v-model="form.tencent_captcha_app_id"
                      type="text"
                      inputmode="numeric"
                      placeholder="123456789"
                      class="w-full font-mono"
                    />
                  </UFormField>
                  <UFormField
                    :label="t('admin.settings.tencentCaptcha.appSecretKey')"
                    :description="form.tencent_captcha_app_secret_key_configured ? t('admin.settings.tencentCaptcha.configured') : t('admin.settings.tencentCaptcha.required')"
                  >
                    <UInput
                      v-model="form.tencent_captcha_app_secret_key"
                      type="password"
                      autocomplete="new-password"
                      :placeholder="t('admin.settings.tencentCaptcha.keepExisting')"
                      class="w-full font-mono"
                    />
                  </UFormField>
                  <div class="border-t border-default pt-5 md:col-span-2">
                    <h3 class="text-sm font-semibold text-highlighted">
                      {{ t("admin.settings.tencentCaptcha.cloudCredentialsTitle") }}
                    </h3>
                    <p class="mt-1 text-xs text-muted">
                      {{ t("admin.settings.tencentCaptcha.cloudCredentialsHint") }}
                    </p>
                  </div>
                  <UFormField
                    :label="t('admin.settings.tencentCaptcha.cloudSecretId')"
                    :description="form.tencent_captcha_cloud_secret_id_configured ? t('admin.settings.tencentCaptcha.configured') : t('admin.settings.tencentCaptcha.required')"
                  >
                    <UInput
                      v-model="form.tencent_captcha_cloud_secret_id"
                      type="password"
                      autocomplete="new-password"
                      :placeholder="t('admin.settings.tencentCaptcha.keepExisting')"
                      class="w-full font-mono"
                    />
                  </UFormField>
                  <UFormField
                    :label="t('admin.settings.tencentCaptcha.cloudSecretKey')"
                    :description="form.tencent_captcha_cloud_secret_key_configured ? t('admin.settings.tencentCaptcha.configured') : t('admin.settings.tencentCaptcha.required')"
                  >
                    <UInput
                      v-model="form.tencent_captcha_cloud_secret_key"
                      type="password"
                      autocomplete="new-password"
                      :placeholder="t('admin.settings.tencentCaptcha.keepExisting')"
                      class="w-full font-mono"
                    />
                  </UFormField>
                </UFormField>
                <p class="text-xs text-muted">
                  {{ t("admin.settings.tencentCaptcha.camPermissionHint") }}
                </p>
                <p class="text-xs text-muted">
                  {{ t("admin.settings.tencentCaptcha.aidEncryptedHint") }}
                </p>
                <div class="flex flex-wrap gap-x-4 gap-y-2 text-sm">
                  <a
                    :href="tencentCaptchaLinks.console"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="text-primary hover:underline"
                  >
                    {{ t("admin.settings.tencentCaptcha.openCaptchaConsole") }}
                  </a>
                  <a
                    :href="tencentCaptchaLinks.cloudKeys"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="text-primary hover:underline"
                  >
                    {{ t("admin.settings.tencentCaptcha.createCloudKeys") }}
                  </a>
                  <a
                    :href="tencentCaptchaLinks.webDocs"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="text-primary hover:underline"
                  >
                    {{ t("admin.settings.tencentCaptcha.openWebDocs") }}
                  </a>
                </div>
              </template>
              <!-- Aliyun Captcha 2.0 fields -->
              <div v-else class="grid grid-cols-1 gap-6">
                <div class="grid grid-cols-1 gap-6 sm:grid-cols-2">
                  <UFormField
                    :label="t('admin.settings.aliyunCaptcha.region')"
                    :description="t('admin.settings.aliyunCaptcha.regionHint')"
                  >
                    <div class="grid grid-cols-2 gap-2 rounded-lg bg-elevated p-1">
                      <UButton
                        @click="form.aliyun_captcha_region = 'cn'"
                        :color="form.aliyun_captcha_region !== 'sgp' ? 'primary' : 'neutral'"
                        :variant="form.aliyun_captcha_region !== 'sgp' ? 'subtle' : 'outline'"
                        size="sm"
                        class="justify-center"
                      >
                        {{ t("admin.settings.aliyunCaptcha.regionCn") }}
                      </UButton>
                      <UButton
                        @click="form.aliyun_captcha_region = 'sgp'"
                        :color="form.aliyun_captcha_region === 'sgp' ? 'primary' : 'neutral'"
                        :variant="form.aliyun_captcha_region === 'sgp' ? 'subtle' : 'outline'"
                        size="sm"
                        class="justify-center"
                      >
                        {{ t("admin.settings.aliyunCaptcha.regionSgp") }}
                      </UButton>
                    </div>
                  </UFormField>
                  <UFormField
                    :label="t('admin.settings.aliyunCaptcha.prefix')"
                    :description="t('admin.settings.aliyunCaptcha.prefixHint')"
                  >
                    <UInput
                      v-model="form.aliyun_captcha_prefix"
                      type="text"
                      placeholder="14xxxxx"
                      class="w-full font-mono"
                    />
                  </UFormField>
                </div>
                <UFormField
                  :label="t('admin.settings.aliyunCaptcha.sceneId')"
                  :description="t('admin.settings.aliyunCaptcha.sceneIdHint')"
                >
                  <UInput
                    v-model="form.aliyun_captcha_scene_id"
                    type="text"
                    placeholder="1cxxxxxx"
                    class="w-full font-mono"
                  />
                </UFormField>
                <UFormField
                  :label="t('admin.settings.aliyunCaptcha.accessKeyId')"
                  :description="t('admin.settings.aliyunCaptcha.accessKeyIdHint')"
                >
                  <UInput
                    v-model="form.aliyun_captcha_access_key_id"
                    type="text"
                    placeholder="LTAI..."
                    class="w-full font-mono"
                  />
                </UFormField>
                <UFormField
                  :label="t('admin.settings.aliyunCaptcha.accessKeySecret')"
                  :description="form.aliyun_captcha_access_key_secret_configured
                        ? t(
                            'admin.settings.aliyunCaptcha.accessKeySecretConfiguredHint',
                          )
                        : t('admin.settings.aliyunCaptcha.accessKeySecretHint')"
                >
                  <UInput
                    v-model="form.aliyun_captcha_access_key_secret"
                    type="password"
                    autocomplete="new-password"
                    placeholder="••••••••"
                    class="w-full font-mono"
                  />
                </UFormField>
              </div>
            </template>
          </SettingsSection>
          <!-- LinuxDo Connect OAuth 登录 -->
          <SettingsSection
            id="settings-linuxdo"
            :title="t('admin.settings.linuxdo.title')"
            :description="t('admin.settings.linuxdo.description')"
          >
            <template #actions>
              <span class="inline-flex size-9 items-center justify-center rounded-lg bg-elevated" aria-hidden="true">
                <svg class="size-5" viewBox="0 0 16 16">
                  <path fill="#EFEFEF" d="m7.44,0s.09,0,.13,0c.09,0,.19,0,.28,0,.14,0,.29,0,.43,0,.09,0,.18,0,.27,0q.12,0,.25,0t.26.08c.15.03.29.06.44.08,1.97.38,3.78,1.47,4.95,3.11.04.06.09.12.13.18.67.96,1.15,2.11,1.3,3.28q0,.19.09.26c0,.15,0,.29,0,.44,0,.04,0,.09,0,.13,0,.09,0,.19,0,.28,0,.14,0,.29,0,.43,0,.09,0,.18,0,.27,0,.08,0,.17,0,.25q0,.19-.08.26c-.03.15-.06.29-.08.44-.38,1.97-1.47,3.78-3.11,4.95-.06.04-.12.09-.18.13-.96.67-2.11,1.15-3.28,1.3q-.19,0-.26.09c-.15,0-.29,0-.44,0-.04,0-.09,0-.13,0-.09,0-.19,0-.28,0-.14,0-.29,0-.43,0-.09,0-.18,0-.27,0-.08,0-.17,0-.25,0q-.19,0-.26-.08c-.15-.03-.29-.06-.44-.08-1.97-.38-3.78-1.47-4.95-3.11q-.07-.09-.13-.18c-.67-.96-1.15-2.11-1.3-3.28q0-.19-.09-.26c0-.15,0-.29,0-.44,0-.04,0-.09,0-.13,0-.09,0-.19,0-.28,0-.14,0-.29,0-.43,0-.09,0-.18,0-.27,0-.08,0-.17,0-.25q0-.19.08-.26c.03-.15.06-.29.08-.44.38-1.97,1.47-3.78,3.11-4.95.06-.04.12-.09.18-.13C4.42.73,5.57.26,6.74.1,7,.07,7.15,0,7.44,0Z" />
                  <path fill="#FEB005" d="m1.27,11.33h13.45c-.94,1.89-2.51,3.21-4.51,3.88-1.99.59-3.96.37-5.8-.57-1.25-.7-2.67-1.9-3.14-3.3Z" />
                  <path fill="#1D1D1F" d="m12.54,1.99c.87.7,1.82,1.59,2.18,2.68H1.27c.87-1.74,2.33-3.13,4.2-3.78,2.44-.79,5-.47,7.07,1.1Z" />
                </svg>
              </span>
            </template>
            <UFormField
              :label="t('admin.settings.linuxdo.enable')"
              :description="t('admin.settings.linuxdo.enableHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.linuxdo_connect_enabled" />
            </UFormField>
            <template v-if="form.linuxdo_connect_enabled">
              <div class="grid grid-cols-1 gap-6">
                <UFormField
                  :label="t('admin.settings.linuxdo.clientId')"
                  :description="t('admin.settings.linuxdo.clientIdHint')"
                >
                  <UInput
                    v-model="form.linuxdo_connect_client_id"
                    type="text"
                    :placeholder="
                      t('admin.settings.linuxdo.clientIdPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField
                  :label="t('admin.settings.linuxdo.clientSecret')"
                  :description="form.linuxdo_connect_client_secret_configured
                        ? t(
                            'admin.settings.linuxdo.clientSecretConfiguredHint',
                          )
                        : t('admin.settings.linuxdo.clientSecretHint')"
                >
                  <UInput
                    v-model="form.linuxdo_connect_client_secret"
                    type="password"
                    :placeholder="
                      form.linuxdo_connect_client_secret_configured
                        ? t(
                            'admin.settings.linuxdo.clientSecretConfiguredPlaceholder',
                          )
                        : t('admin.settings.linuxdo.clientSecretPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField
                  :label="t('admin.settings.linuxdo.redirectUrl')"
                  :description="t('admin.settings.linuxdo.redirectUrlHint')"
                >
                  <UInput
                    v-model="form.linuxdo_connect_redirect_url"
                    type="url"
                    :placeholder="
                      t('admin.settings.linuxdo.redirectUrlPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                  <div class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3">
                    <UButton
                      @click="setAndCopyLinuxdoRedirectUrl"
                      color="neutral"
                      variant="outline"
                      size="sm"
                      class="w-fit"
                    >
                      {{ t("admin.settings.linuxdo.quickSetCopy") }}
                    </UButton>
                    <code
                      v-if="linuxdoRedirectUrlSuggestion"
                      class="select-all break-all rounded-sm bg-elevated px-2 py-1 font-mono text-xs text-toned"
                    >
                      {{ linuxdoRedirectUrlSuggestion }}
                    </code>
                  </div>
                </UFormField>
              </div>
            </template>
          </SettingsSection>

          <!-- GitHub / Google 邮箱快捷登录 -->
          <SettingsSection
            id="settings-email-oauth"
            :title="localText('邮箱快捷登录', 'Email OAuth Sign-in')"
            :description="localText(
                    '开启 GitHub 或 Google 邮箱授权登录后，系统会读取已验证邮箱，存在则直接登录，不存在则自动注册。',
                    'After GitHub or Google email OAuth is enabled, the system reads a verified email, signs in matching users, and auto-registers missing users.',
                  )"
          >
            <template #actions>
              <span class="flex items-center gap-1.5">
                <span class="inline-flex size-9 items-center justify-center rounded-lg bg-elevated" aria-hidden="true">
                  <GitHubMark class="size-5" />
                </span>
                <span class="inline-flex size-9 items-center justify-center rounded-lg bg-elevated" aria-hidden="true">
                  <GoogleMark class="size-5" />
                </span>
              </span>
            </template>
            <div class="grid grid-cols-1 gap-6 xl:grid-cols-2">
              <div class="">
                <UFormField
                  label="GitHub"
                  :description="localText(
                          'GitHub OAuth App 需要 read:user user:email 权限，回调地址填写下方后端地址。',
                          'GitHub OAuth App needs read:user user:email scopes. Use the backend callback URL below.',
                        )"
                  class="flex items-center justify-between gap-4"
                >
                  <Toggle v-model="form.github_oauth_enabled" />
                </UFormField>

                <div v-if="form.github_oauth_enabled" class="mt-4 space-y-4">
                  <div class="rounded-lg bg-elevated px-3 py-2 text-xs text-toned">
                    <template v-if="isZhLocale">
                      开通引导：GitHub Settings → Developer settings →
                      <a
                        data-testid="github-oauth-apps-guide-link"
                        href="https://github.com/settings/developers"
                        target="_blank"
                        rel="noopener noreferrer"
                        class="font-medium text-primary hover:underline"
                      >OAuth Apps</a>
                      → New OAuth App；Homepage URL 填站点域名，Authorization callback URL 填下面的后端回调地址。
                    </template>
                    <template v-else>
                      Setup guide: GitHub Settings → Developer settings →
                      <a
                        data-testid="github-oauth-apps-guide-link"
                        href="https://github.com/settings/developers"
                        target="_blank"
                        rel="noopener noreferrer"
                        class="font-medium text-primary hover:underline"
                      >OAuth Apps</a>
                      → New OAuth App. Use your site origin as Homepage URL and the backend callback URL below as Authorization callback URL.
                    </template>
                  </div>

                  <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
                    <UFormField label="Client ID">
                      <UInput
                        v-model="form.github_oauth_client_id"
                        type="text"
                        placeholder="GitHub OAuth Client ID"
                        class="w-full font-mono"
                      />
                    </UFormField>
                    <UFormField label="Client Secret">
                      <UInput
                        v-model="form.github_oauth_client_secret"
                        type="password"
                        :placeholder="
                          form.github_oauth_client_secret_configured
                            ? localText('密钥已配置，留空以保留当前值。', 'Secret configured. Leave empty to keep the current value.')
                            : 'GitHub OAuth Client Secret'
                        "
                        class="w-full font-mono"
                      />
                    </UFormField>
                  </div>

                  <UFormField :label="localText('后端回调地址', 'Backend Callback URL')">
                    <UInput
                      v-model="form.github_oauth_redirect_url"
                      type="url"
                      placeholder="https://your-domain.com/api/v1/auth/oauth/github/callback"
                      class="w-full font-mono"
                    />
                    <div class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3">
                      <UButton
                        @click="setAndCopyEmailOAuthRedirectUrl('github')"
                        color="neutral"
                        variant="outline"
                        size="sm"
                        class="w-fit"
                      >
                        {{ localText("生成并复制", "Generate and copy") }}
                      </UButton>
                      <code
                        v-if="githubOAuthRedirectUrlSuggestion"
                        class="select-all break-all rounded-sm bg-elevated px-2 py-1 font-mono text-xs text-toned"
                      >
                        {{ githubOAuthRedirectUrlSuggestion }}
                      </code>
                    </div>
                  </UFormField>

                  <UFormField :label="localText('前端回跳地址', 'Frontend Callback URL')">
                    <UInput
                      v-model="form.github_oauth_frontend_redirect_url"
                      type="text"
                      placeholder="/auth/oauth/callback"
                      class="w-full font-mono"
                    />
                  </UFormField>
                </div>
              </div>

              <div class="">
                <UFormField
                  label="Google"
                  :description="localText(
                          'Google OAuth 客户端需要 openid email profile 范围，并在凭据里登记后端回调地址。',
                          'Google OAuth client needs openid email profile scopes and the backend callback URL registered in credentials.',
                        )"
                  class="flex items-center justify-between gap-4"
                >
                  <Toggle v-model="form.google_oauth_enabled" />
                </UFormField>

                <div v-if="form.google_oauth_enabled" class="mt-4 space-y-4">
                  <div class="rounded-lg bg-elevated px-3 py-2 text-xs text-toned">
                    {{
                      localText(
                        "开通引导：Google Cloud Console → APIs & Services → OAuth consent screen 完成同意屏幕；Credentials → Create Credentials → OAuth client ID，类型选择 Web application，并把下面地址加入 Authorized redirect URIs。",
                        "Setup guide: Google Cloud Console → APIs & Services → OAuth consent screen, then Credentials → Create Credentials → OAuth client ID, choose Web application, and add the URL below to Authorized redirect URIs.",
                      )
                    }}
                  </div>

                  <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
                    <UFormField label="Client ID">
                      <UInput
                        v-model="form.google_oauth_client_id"
                        type="text"
                        placeholder="Google OAuth Client ID"
                        class="w-full font-mono"
                      />
                    </UFormField>
                    <UFormField label="Client Secret">
                      <UInput
                        v-model="form.google_oauth_client_secret"
                        type="password"
                        :placeholder="
                          form.google_oauth_client_secret_configured
                            ? localText('密钥已配置，留空以保留当前值。', 'Secret configured. Leave empty to keep the current value.')
                            : 'Google OAuth Client Secret'
                        "
                        class="w-full font-mono"
                      />
                    </UFormField>
                  </div>

                  <UFormField :label="localText('后端回调地址', 'Backend Callback URL')">
                    <UInput
                      v-model="form.google_oauth_redirect_url"
                      type="url"
                      placeholder="https://your-domain.com/api/v1/auth/oauth/google/callback"
                      class="w-full font-mono"
                    />
                    <div class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3">
                      <UButton
                        @click="setAndCopyEmailOAuthRedirectUrl('google')"
                        color="neutral"
                        variant="outline"
                        size="sm"
                        class="w-fit"
                      >
                        {{ localText("生成并复制", "Generate and copy") }}
                      </UButton>
                      <code
                        v-if="googleOAuthRedirectUrlSuggestion"
                        class="select-all break-all rounded-sm bg-elevated px-2 py-1 font-mono text-xs text-toned"
                      >
                        {{ googleOAuthRedirectUrlSuggestion }}
                      </code>
                    </div>
                  </UFormField>

                  <UFormField :label="localText('前端回跳地址', 'Frontend Callback URL')">
                    <UInput
                      v-model="form.google_oauth_frontend_redirect_url"
                      type="text"
                      placeholder="/auth/oauth/callback"
                      class="w-full font-mono"
                    />
                  </UFormField>
                </div>
              </div>
            </div>
          </SettingsSection>

          <!-- WeChat Connect OAuth 登录 -->
          <SettingsSection
            id="settings-wechat"
            :title="t('admin.settings.wechatConnect.title')"
            :description="t('admin.settings.wechatConnect.description')"
          >
            <template #actions>
              <span class="inline-flex size-9 items-center justify-center rounded-lg bg-elevated" aria-hidden="true">
                <svg class="size-5" viewBox="0 0 24 24">
                  <path fill="#07C160" d="M9.4 3.6c-4.3 0-7.8 2.9-7.8 6.5 0 2.1 1.2 4 3.1 5.2l-.8 2.4 2.7-1.4c.8.2 1.7.4 2.6.4h.4a5.4 5.4 0 0 1-.2-1.4c0-3.4 3.2-6.1 7.2-6.1h.5c-.8-3.2-3.9-5.6-7.7-5.6Z" />
                  <path fill="#07C160" opacity=".85" d="M15.8 10.1c-3.4 0-6.2 2.2-6.2 5s2.8 5 6.2 5c.7 0 1.4-.1 2.1-.3l2.2 1.1-.6-1.9c1.2-1 1.9-2.3 1.9-3.9 0-2.8-2.8-5-5.6-5Z" />
                </svg>
              </span>
            </template>
            <UFormField
              :label="t('admin.settings.wechatConnect.enabledLabel')"
              :description="t('admin.settings.wechatConnect.enabledHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.wechat_connect_enabled" data-testid="wechat-connect-enabled" />
            </UFormField>
            <template v-if="form.wechat_connect_enabled">
              <UFormField
                :label="localText('PC 应用', 'PC App')"
                :description="localText(
                        '桌面浏览器通过微信开放平台扫码登录。可与公众号或移动应用同时存在。',
                        'Desktop browsers sign in through WeChat Open Platform QR login. This can coexist with Official Account or Mobile App.',
                      )"
                class="flex items-center justify-between gap-4"
              >
                <Toggle
                  :model-value="form.wechat_connect_open_enabled"
                  data-testid="wechat-connect-open-enabled"
                  @update:model-value="handleWeChatOpenEnabledChange"
                />
              </UFormField>
              <div v-if="form.wechat_connect_open_enabled" class="grid grid-cols-1 gap-4 lg:grid-cols-2">
                <UFormField :label="localText('PC AppID', 'PC App ID')">
                  <UInput
                    v-model="form.wechat_connect_open_app_id"
                    data-testid="wechat-connect-open-app-id"
                    type="text"
                    :placeholder="
                      localText(
                        '微信开放平台 PC 应用 AppID',
                        'WeChat Open Platform PC App ID',
                      )
                    "
                    class="w-full font-mono"
                  />
                </UFormField>
                <UFormField :label="localText('PC AppSecret', 'PC App Secret')">
                  <UInput
                    v-model="form.wechat_connect_open_app_secret"
                    data-testid="wechat-connect-open-app-secret"
                    type="password"
                    :placeholder="
                      form.wechat_connect_open_app_secret_configured
                        ? localText(
                            '密钥已配置，留空以保留当前值。',
                            'Secret configured. Leave empty to keep the current value.',
                          )
                        : localText(
                            '微信开放平台 PC 应用 AppSecret',
                            'WeChat Open Platform PC App Secret',
                          )
                    "
                    class="w-full font-mono"
                  />
                </UFormField>
              </div>
              <UFormField
                :label="localText('公众号', 'Official Account')"
                :description="localText(
                        '仅在微信内浏览器可用；非微信环境下会显示不可用。',
                        'Only available inside the WeChat browser. It is shown as unavailable outside WeChat.',
                      )"
                class="flex items-center justify-between gap-4"
              >
                <Toggle
                  :model-value="form.wechat_connect_mp_enabled"
                  data-testid="wechat-connect-mp-enabled"
                  @update:model-value="handleWeChatMPEnabledChange"
                />
              </UFormField>
              <div v-if="form.wechat_connect_mp_enabled" class="grid grid-cols-1 gap-4 lg:grid-cols-2">
                <UFormField :label="localText('公众号 AppID', 'Official Account App ID')">
                  <UInput
                    v-model="form.wechat_connect_mp_app_id"
                    data-testid="wechat-connect-mp-app-id"
                    type="text"
                    :placeholder="
                      localText(
                        '公众号 AppID',
                        'Official Account App ID',
                      )
                    "
                    class="w-full font-mono"
                  />
                </UFormField>
                <UFormField
                  :label="localText(
                        '公众号 AppSecret',
                        'Official Account App Secret',
                      )"
                >
                  <UInput
                    v-model="form.wechat_connect_mp_app_secret"
                    data-testid="wechat-connect-mp-app-secret"
                    type="password"
                    :placeholder="
                      form.wechat_connect_mp_app_secret_configured
                        ? localText(
                            '密钥已配置，留空以保留当前值。',
                            'Secret configured. Leave empty to keep the current value.',
                          )
                        : localText(
                            '公众号 AppSecret',
                            'Official Account App Secret',
                          )
                    "
                    class="w-full font-mono"
                  />
                </UFormField>
              </div>
              <UFormField
                :label="localText('移动应用', 'Mobile App')"
                :description="localText(
                        '原生移动端通过微信 SDK 唤起授权，网页端不会直接发起该流程。',
                        'Native mobile clients start authorization through the WeChat SDK. The web UI does not launch this flow directly.',
                      )"
                class="flex items-center justify-between gap-4"
              >
                <Toggle
                  :model-value="form.wechat_connect_mobile_enabled"
                  data-testid="wechat-connect-mobile-enabled"
                  @update:model-value="handleWeChatMobileEnabledChange"
                />
              </UFormField>
              <div v-if="form.wechat_connect_mobile_enabled" class="grid grid-cols-1 gap-4 lg:grid-cols-2">
                <UFormField :label="localText('移动应用 AppID', 'Mobile App ID')">
                  <UInput
                    v-model="form.wechat_connect_mobile_app_id"
                    data-testid="wechat-connect-mobile-app-id"
                    type="text"
                    :placeholder="
                      localText(
                        '移动应用 AppID',
                        'Mobile App ID',
                      )
                    "
                    class="w-full font-mono"
                  />
                </UFormField>
                <UFormField :label="localText('移动应用 AppSecret', 'Mobile App Secret')">
                  <UInput
                    v-model="form.wechat_connect_mobile_app_secret"
                    data-testid="wechat-connect-mobile-app-secret"
                    type="password"
                    :placeholder="
                      form.wechat_connect_mobile_app_secret_configured
                        ? localText(
                            '密钥已配置，留空以保留当前值。',
                            'Secret configured. Leave empty to keep the current value.',
                          )
                        : localText(
                            '移动应用 AppSecret',
                            'Mobile App Secret',
                          )
                    "
                    class="w-full font-mono"
                  />
                </UFormField>
              </div>
              <UAlert
                v-if="
                  form.wechat_connect_open_enabled &&
                  (form.wechat_connect_mp_enabled ||
                    form.wechat_connect_mobile_enabled)
                "
                color="warning"
                variant="subtle"
                icon="i-lucide-triangle-alert"
              >
                <template #description>

                </template>
              </UAlert>
              <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
                <UFormField
                  :label="localText(
                        '浏览器回调地址',
                        'Browser Redirect URL',
                      )"
                  :description="localText(
                        '用于 PC 应用和公众号的网页回调。移动应用走原生 SDK 时不直接使用这个浏览器回调。',
                        'Used by PC App and Official Account browser callbacks. Native mobile SDK flows do not start from this browser callback directly.',
                      )"
                >
                  <UInput
                    data-testid="wechat-connect-redirect-url"
                    v-model="form.wechat_connect_redirect_url"
                    type="url"
                    :placeholder="t('admin.settings.wechatConnect.redirectUrlPlaceholder')"
                    class="w-full font-mono"
                  />
                  <div class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3">
                    <UButton
                      @click="setAndCopyWeChatRedirectUrl"
                      color="neutral"
                      variant="outline"
                      size="sm"
                      class="w-fit"
                    >
                      {{ t("admin.settings.wechatConnect.generateAndCopy") }}
                    </UButton>
                    <code
                      v-if="wechatRedirectUrlSuggestion"
                      class="select-all break-all rounded-sm bg-elevated px-2 py-1 font-mono text-xs text-toned"
                    >
                      {{ wechatRedirectUrlSuggestion }}
                    </code>
                  </div>
                </UFormField>
              </div>
              <UFormField
                :label="t('admin.settings.wechatConnect.frontendRedirectUrlLabel')"
                :description="t('admin.settings.wechatConnect.frontendRedirectUrlHint')"
                class="flex max-sm:flex-col justify-between items-start gap-4"
                :ui="{ container: 'w-full sm:max-w-sm' }"
              >
                <UInput
                  data-testid="wechat-connect-frontend-redirect-url"
                  v-model="form.wechat_connect_frontend_redirect_url"
                  type="text"
                  :placeholder="t('admin.settings.wechatConnect.frontendRedirectUrlPlaceholder')"
                  class="w-full font-mono"
                />
              </UFormField>
            </template>
          </SettingsSection>

          <!-- DingTalk Connect OAuth 登录 -->
          <SettingsSection
            id="settings-dingtalk"
            :title="t('admin.settings.dingtalk.title')"
            :description="t('admin.settings.dingtalk.description')"
          >
            <template #actions>
              <span class="inline-flex size-9 items-center justify-center rounded-lg bg-elevated" aria-hidden="true">
                <svg class="size-5" viewBox="0 0 24 24">
                  <rect width="24" height="24" rx="6" fill="#1677FF" />
                  <path fill="#FFFFFF" d="M7.4 6.2h5a4.2 4.2 0 0 1 0 8.4h-2.2v3.2H7.4V6.2Zm2.8 2.4v3.6h2a1.8 1.8 0 0 0 0-3.6h-2Z" />
                </svg>
              </span>
            </template>
            <UFormField
              :label="t('admin.settings.dingtalk.enable')"
              :description="t('admin.settings.dingtalk.enableHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.dingtalk_connect_enabled" />
            </UFormField>
            <template v-if="form.dingtalk_connect_enabled">
              <div class="grid grid-cols-1 gap-6">
                <UFormField
                  :label="t('admin.settings.dingtalk.clientId')"
                  :description="t('admin.settings.dingtalk.clientIdHint')"
                >
                  <UInput
                    v-model="form.dingtalk_connect_client_id"
                    type="text"
                    :placeholder="
                      t('admin.settings.dingtalk.clientIdPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField
                  :label="t('admin.settings.dingtalk.clientSecret')"
                  :description="form.dingtalk_connect_client_secret_configured
                        ? t(
                            'admin.settings.dingtalk.clientSecretConfiguredHint',
                          )
                        : t('admin.settings.dingtalk.clientSecretHint')"
                >
                  <UInput
                    v-model="form.dingtalk_connect_client_secret"
                    type="password"
                    :placeholder="
                      form.dingtalk_connect_client_secret_configured
                        ? t(
                            'admin.settings.dingtalk.clientSecretConfiguredPlaceholder',
                          )
                        : t('admin.settings.dingtalk.clientSecretPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField
                  :label="t('admin.settings.dingtalk.redirectUrl')"
                  :description="t('admin.settings.dingtalk.redirectUrlHint')"
                >
                  <UInput
                    v-model="form.dingtalk_connect_redirect_url"
                    type="url"
                    :placeholder="
                      t('admin.settings.dingtalk.redirectUrlPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <!-- Corp Restriction Policy -->
                <UFormField
                  :label="t('admin.settings.dingtalk.corpPolicy.label')"
                  :description="t('admin.settings.dingtalk.corpPolicy.hint')"
                >
                  <URadioGroup
                    v-model="form.dingtalk_connect_corp_restriction_policy"
                    :items="[{ value: 'none', label: t('admin.settings.dingtalk.corpPolicy.none') }, { value: 'internal_only', label: t('admin.settings.dingtalk.corpPolicy.internalOnly') }]"
                  />
                </UFormField>

                <!-- bypass_registration toggle（仅 internal_only 模式下可见可用） -->
                <UFormField
                  v-if="form.dingtalk_connect_corp_restriction_policy === 'internal_only'"
                  :label="t('admin.settings.dingtalk.bypassRegistration')"
                  :description="t('admin.settings.dingtalk.bypassRegistrationHint')"
                  class="flex items-center justify-between gap-4"
                >
                  <Toggle v-model="form.dingtalk_connect_bypass_registration" />
                </UFormField>

                <!-- 身份同步开关（仅 internal_only 模式下可见） -->
                <div
                  v-if="form.dingtalk_connect_corp_restriction_policy === 'internal_only'"
                  class="pt-4 border-t border-default space-y-2"
                >
                  <UFormField
                    :label="t('admin.settings.dingtalk.syncDisplayName')"
                    :description="t('admin.settings.dingtalk.syncDisplayNameHint')"
                    class="flex items-center justify-between gap-4"
                  >
                    <Toggle v-model="form.dingtalk_connect_sync_display_name" />
                  </UFormField>
                  <div v-if="form.dingtalk_connect_sync_display_name" class="space-y-2">
                    <UFormField :label="t('admin.settings.dingtalk.syncDisplayNameTarget')">
                      <UInput
                        v-model="form.dingtalk_connect_sync_display_name_attr_key"
                        type="text"
                        placeholder="dingtalk_name"
                        class="flex-1 max-w-xs"
                      />
                    </UFormField>
                    <UFormField :label="t('admin.settings.dingtalk.syncAttrDisplayName')">
                      <UInput
                        v-model="form.dingtalk_connect_sync_display_name_attr_name"
                        type="text"
                        :placeholder="localText('钉钉姓名', 'DingTalk Name')"
                        class="flex-1 max-w-xs"
                      />
                    </UFormField>
                  </div>
                  <p v-if="form.dingtalk_connect_sync_display_name" class="text-xs text-dimmed">
                    {{ t("admin.settings.dingtalk.syncDisplayNameTargetHint") }}
                  </p>
                </div>
                <div
                  v-if="form.dingtalk_connect_corp_restriction_policy === 'internal_only'"
                  class="pt-4 border-t border-default space-y-2"
                >
                  <UFormField
                    :label="t('admin.settings.dingtalk.syncCorpEmail')"
                    :description="t('admin.settings.dingtalk.syncCorpEmailHint')"
                    class="flex items-center justify-between gap-4"
                  >
                    <Toggle v-model="form.dingtalk_connect_sync_corp_email" />
                    <p class="text-xs text-warning mt-2">
                      {{ t("admin.settings.dingtalk.syncCorpEmailPermissionHint") }}
                    </p>
                  </UFormField>
                  <div v-if="form.dingtalk_connect_sync_corp_email" class="space-y-2">
                    <UFormField :label="t('admin.settings.dingtalk.syncCorpEmailTarget')">
                      <UInput
                        v-model="form.dingtalk_connect_sync_corp_email_attr_key"
                        type="text"
                        placeholder="dingtalk_email"
                        class="flex-1 max-w-xs"
                      />
                    </UFormField>
                    <UFormField :label="t('admin.settings.dingtalk.syncAttrDisplayName')">
                      <UInput
                        v-model="form.dingtalk_connect_sync_corp_email_attr_name"
                        type="text"
                        :placeholder="localText('钉钉企业邮箱', 'DingTalk Corporate Email')"
                        class="flex-1 max-w-xs"
                      />
                    </UFormField>
                  </div>
                  <p v-if="form.dingtalk_connect_sync_corp_email" class="text-xs text-dimmed">
                    {{ t("admin.settings.dingtalk.syncCorpEmailTargetHint") }}
                  </p>
                </div>
                <div
                  v-if="form.dingtalk_connect_corp_restriction_policy === 'internal_only'"
                  class="pt-4 border-t border-default space-y-2"
                >
                  <UFormField
                    :label="t('admin.settings.dingtalk.syncDept')"
                    :description="t('admin.settings.dingtalk.syncDeptHint')"
                    class="flex items-center justify-between gap-4"
                  >
                    <Toggle v-model="form.dingtalk_connect_sync_dept" />
                    <p class="text-xs text-warning mt-2">
                      {{ t("admin.settings.dingtalk.syncDeptPermissionHint") }}
                    </p>
                  </UFormField>
                  <div v-if="form.dingtalk_connect_sync_dept" class="space-y-2">
                    <UFormField :label="t('admin.settings.dingtalk.syncDeptTarget')">
                      <UInput
                        v-model="form.dingtalk_connect_sync_dept_attr_key"
                        type="text"
                        placeholder="dingtalk_department"
                        class="flex-1 max-w-xs"
                      />
                    </UFormField>
                    <UFormField :label="t('admin.settings.dingtalk.syncAttrDisplayName')">
                      <UInput
                        v-model="form.dingtalk_connect_sync_dept_attr_name"
                        type="text"
                        :placeholder="localText('钉钉部门', 'DingTalk Department')"
                        class="flex-1 max-w-xs"
                      />
                    </UFormField>
                  </div>
                  <p v-if="form.dingtalk_connect_sync_dept" class="text-xs text-dimmed">
                    {{ t("admin.settings.dingtalk.syncDeptTargetHint") }}
                  </p>
                </div>
              </div>
            </template>
          </SettingsSection>

          <!-- Generic OIDC OAuth 登录 -->
          <SettingsSection id="settings-oidc" :title="t('admin.settings.oidc.title')" :description="t('admin.settings.oidc.description')">
            <template #actions>
              <span class="inline-flex size-9 items-center justify-center rounded-lg bg-elevated" aria-hidden="true">
                <UIcon name="i-lucide-fingerprint" class="size-5 text-muted" />
              </span>
            </template>
            <UFormField
              :label="t('admin.settings.oidc.enable')"
              :description="t('admin.settings.oidc.enableHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.oidc_connect_enabled" />
            </UFormField>
            <template v-if="form.oidc_connect_enabled">
              <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
                <UFormField :label="t('admin.settings.oidc.providerName')">
                  <UInput
                    v-model="form.oidc_connect_provider_name"
                    type="text"
                    :placeholder="
                      t('admin.settings.oidc.providerNamePlaceholder')
                    "
                    class="w-full"
                  />
                </UFormField>

                <UFormField :label="t('admin.settings.oidc.clientId')">
                  <UInput
                    v-model="form.oidc_connect_client_id"
                    type="text"
                    :placeholder="
                      t('admin.settings.oidc.clientIdPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField
                  :label="t('admin.settings.oidc.clientSecret')"
                  :description="form.oidc_connect_client_secret_configured
                        ? t('admin.settings.oidc.clientSecretConfiguredHint')
                        : t('admin.settings.oidc.clientSecretHint')"
                >
                  <UInput
                    v-model="form.oidc_connect_client_secret"
                    type="password"
                    :placeholder="
                      form.oidc_connect_client_secret_configured
                        ? t(
                            'admin.settings.oidc.clientSecretConfiguredPlaceholder',
                          )
                        : t('admin.settings.oidc.clientSecretPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>
              </div>
              <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
                <UFormField :label="t('admin.settings.oidc.issuerUrl')">
                  <UInput
                    v-model="form.oidc_connect_issuer_url"
                    type="url"
                    :placeholder="
                      t('admin.settings.oidc.issuerUrlPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField :label="t('admin.settings.oidc.discoveryUrl')">
                  <UInput
                    v-model="form.oidc_connect_discovery_url"
                    type="url"
                    :placeholder="
                      t('admin.settings.oidc.discoveryUrlPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField :label="t('admin.settings.oidc.authorizeUrl')">
                  <UInput
                    v-model="form.oidc_connect_authorize_url"
                    type="url"
                    :placeholder="
                      t('admin.settings.oidc.authorizeUrlPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField :label="t('admin.settings.oidc.tokenUrl')">
                  <UInput
                    v-model="form.oidc_connect_token_url"
                    type="url"
                    :placeholder="
                      t('admin.settings.oidc.tokenUrlPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField :label="t('admin.settings.oidc.userinfoUrl')">
                  <UInput
                    v-model="form.oidc_connect_userinfo_url"
                    type="url"
                    :placeholder="
                      t('admin.settings.oidc.userinfoUrlPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField :label="t('admin.settings.oidc.jwksUrl')">
                  <UInput
                    v-model="form.oidc_connect_jwks_url"
                    type="url"
                    :placeholder="t('admin.settings.oidc.jwksUrlPlaceholder')"
                    class="w-full font-mono"
                  />
                </UFormField>
              </div>
              <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
                <UFormField
                  :label="t('admin.settings.oidc.scopes')"
                  :description="t('admin.settings.oidc.scopesHint')"
                >
                  <UInput
                    v-model="form.oidc_connect_scopes"
                    type="text"
                    :placeholder="t('admin.settings.oidc.scopesPlaceholder')"
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField
                  :label="t('admin.settings.oidc.redirectUrl')"
                  :description="t('admin.settings.oidc.redirectUrlHint')"
                >
                  <UInput
                    v-model="form.oidc_connect_redirect_url"
                    type="url"
                    :placeholder="
                      t('admin.settings.oidc.redirectUrlPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                  <div class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3">
                    <UButton
                      @click="setAndCopyOIDCRedirectUrl"
                      color="neutral"
                      variant="outline"
                      size="sm"
                      class="w-fit"
                    >
                      {{ t("admin.settings.oidc.quickSetCopy") }}
                    </UButton>
                    <code
                      v-if="oidcRedirectUrlSuggestion"
                      class="select-all break-all rounded-sm bg-elevated px-2 py-1 font-mono text-xs text-toned"
                    >
                      {{ oidcRedirectUrlSuggestion }}
                    </code>
                  </div>
                </UFormField>

                <UFormField
                  :label="t('admin.settings.oidc.frontendRedirectUrl')"
                  :description="t('admin.settings.oidc.frontendRedirectUrlHint')"
                  class="lg:col-span-2"
                >
                  <UInput
                    v-model="form.oidc_connect_frontend_redirect_url"
                    type="text"
                    :placeholder="
                      t('admin.settings.oidc.frontendRedirectUrlPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>
              </div>
              <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
                <UFormField :label="t('admin.settings.oidc.tokenAuthMethod')">
                  <Select
                    v-model="form.oidc_connect_token_auth_method"
                    :options="[{ value: 'client_secret_post', label: 'client_secret_post' }, { value: 'client_secret_basic', label: 'client_secret_basic' }, { value: 'none', label: 'none' }]"
                    class="w-64 font-mono"
                  />
                </UFormField>

                <UFormField :label="t('admin.settings.oidc.clockSkewSeconds')">
                  <UInput
                    v-model.number="form.oidc_connect_clock_skew_seconds"
                    type="number"
                    min="0"
                    max="600"
                    class="w-full"
                  />
                </UFormField>

                <UFormField :label="t('admin.settings.oidc.allowedSigningAlgs')">
                  <UInput
                    v-model="form.oidc_connect_allowed_signing_algs"
                    type="text"
                    :placeholder="
                      t('admin.settings.oidc.allowedSigningAlgsPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>
              </div>
              <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
                <UFormField :label="t('admin.settings.oidc.usePkce')" class="flex items-center justify-between gap-4">
                  <Toggle v-model="form.oidc_connect_use_pkce" data-testid="oidc-connect-use-pkce" />
                </UFormField>

                <UFormField
                  :label="t('admin.settings.oidc.validateIdToken')"
                  class="flex items-center justify-between gap-4"
                >
                  <Toggle v-model="form.oidc_connect_validate_id_token" data-testid="oidc-connect-validate-id-token" />
                </UFormField>

                <UFormField
                  :label="t('admin.settings.oidc.requireEmailVerified')"
                  class="flex items-center justify-between gap-4"
                >
                  <Toggle v-model="form.oidc_connect_require_email_verified" />
                </UFormField>
              </div>
              <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
                <UFormField :label="t('admin.settings.oidc.userinfoEmailPath')">
                  <UInput
                    v-model="form.oidc_connect_userinfo_email_path"
                    type="text"
                    :placeholder="
                      t('admin.settings.oidc.userinfoEmailPathPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField :label="t('admin.settings.oidc.userinfoIdPath')">
                  <UInput
                    v-model="form.oidc_connect_userinfo_id_path"
                    type="text"
                    :placeholder="
                      t('admin.settings.oidc.userinfoIdPathPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField :label="t('admin.settings.oidc.userinfoUsernamePath')">
                  <UInput
                    v-model="form.oidc_connect_userinfo_username_path"
                    type="text"
                    :placeholder="
                      t('admin.settings.oidc.userinfoUsernamePathPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>
              </div>
            </template>
          </SettingsSection>
        </div>
        <!-- /Tab: Security — Registration, Turnstile, LinuxDo, OIDC -->

        <!-- Tab: Users -->
        <div v-show="activeTab === 'users'" class="flex flex-col gap-4 sm:gap-6 lg:gap-12">
          <!-- Default Settings -->
          <SettingsSection
            id="settings-defaults"
            :title="t('admin.settings.defaults.title')"
            :description="t('admin.settings.defaults.description')"
          >
            <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
              <UFormField
                :label="t('admin.settings.defaults.defaultBalance')"
                :description="t('admin.settings.defaults.defaultBalanceHint')"
              >
                <UInput
                  v-model.number="form.default_balance"
                  type="number"
                  step="0.01"
                  min="0"
                  placeholder="0.00"
                  class="w-full"
                />
              </UFormField>
              <UFormField
                :label="t('admin.settings.defaults.defaultConcurrency')"
                :description="t('admin.settings.defaults.defaultConcurrencyHint')"
              >
                <UInput v-model.number="form.default_concurrency" type="number" min="1" placeholder="1" class="w-full" />
              </UFormField>
              <UFormField
                :label="t('admin.settings.defaults.defaultUserRpmLimit')"
                :description="t('admin.settings.defaults.defaultUserRpmLimitHint')"
              >
                <UInput
                  v-model.number="form.default_user_rpm_limit"
                  type="number"
                  min="0"
                  step="1"
                  placeholder="0"
                  class="w-full"
                />
              </UFormField>
            </div>
            <UFormField
              :label="t('admin.settings.defaults.defaultSubscriptions')"
              :description="t('admin.settings.defaults.defaultSubscriptionsHint')"
              class="flex max-sm:flex-col justify-between sm:items-center gap-4"
            >
              <UButton
                @click="addDefaultSubscription"
                :disabled="subscriptionGroups.length === 0"
                color="neutral"
                variant="outline"
                size="sm"
              >
                {{ t("admin.settings.defaults.addDefaultSubscription") }}
              </UButton>
            </UFormField>
            <div
              v-if="form.default_subscriptions.length === 0"
              class="py-2 text-sm text-muted"
            >
              {{ t("admin.settings.defaults.defaultSubscriptionsEmpty") }}
            </div>
            <template v-else>
              <div
                v-for="(item, index) in form.default_subscriptions"
                :key="`default-sub-${index}`"
                class="grid grid-cols-1 gap-3 md:grid-cols-[1fr_160px_auto] border-t border-default pt-4 first:border-t-0 first:pt-0"
              >
                <UFormField :label="t('admin.settings.defaults.subscriptionGroup')">
                  <Select
                    v-model="item.group_id"
                    class="default-sub-group-select"
                    :options="defaultSubscriptionGroupOptions"
                    :placeholder="
                      t('admin.settings.defaults.subscriptionGroup')
                    "
                  >
                    <template #selected="{ option }">
                      <GroupBadge
                        v-if="option"
                        :name="
                          (
                            option as unknown as DefaultSubscriptionGroupOption
                          ).label
                        "
                        :platform="
                          (
                            option as unknown as DefaultSubscriptionGroupOption
                          ).platform
                        "
                        :subscription-type="
                          (
                            option as unknown as DefaultSubscriptionGroupOption
                          ).subscriptionType
                        "
                        :rate-multiplier="
                          (
                            option as unknown as DefaultSubscriptionGroupOption
                          ).rate
                        "
                      />
                      <span v-else class="text-dimmed">
                        {{ t("admin.settings.defaults.subscriptionGroup") }}
                      </span>
                    </template>
                    <template #option="{ option, selected }">
                      <GroupOptionItem
                        :name="
                          (
                            option as unknown as DefaultSubscriptionGroupOption
                          ).label
                        "
                        :platform="
                          (
                            option as unknown as DefaultSubscriptionGroupOption
                          ).platform
                        "
                        :subscription-type="
                          (
                            option as unknown as DefaultSubscriptionGroupOption
                          ).subscriptionType
                        "
                        :rate-multiplier="
                          (
                            option as unknown as DefaultSubscriptionGroupOption
                          ).rate
                        "
                        :description="
                          (
                            option as unknown as DefaultSubscriptionGroupOption
                          ).description
                        "
                        :selected="selected"
                      />
                    </template>
                  </Select>
                </UFormField>
                <UFormField :label="t('admin.settings.defaults.subscriptionValidityDays')">
                  <UInput v-model.number="item.validity_days" type="number" min="1" max="36500" class="w-full" />
                </UFormField>
                <div class="flex items-end">
                  <UButton @click="removeDefaultSubscription(index)" color="error" variant="soft" class="w-full">
                    {{ t("common.delete") }}
                  </UButton>
                </div>
              </div>
            </template>
            <!-- ★ 新增：系统全局默认平台限额矩阵 -->
            <UFormField
              :label="t('admin.settings.defaults.defaultPlatformQuotas')"
              :description="t('admin.settings.defaults.defaultPlatformQuotasHint')"
              :ui="{ container: 'w-full mt-3' }"
            >
              <UTable
                :data="platformQuotaRows"
                :columns="platformQuotaColumns"
                :ui="{ th: 'px-2 py-2 text-xs', td: 'px-2 py-1.5' }"
              >
                <template #platform-cell="{ row: { original: { platform: p } } }">
                  <span class="font-mono text-xs text-toned">{{ p }}</span>
                </template>
                <template #daily-cell="{ row: { original: { platform: p } } }">
                  <UInput v-model.number="form.default_platform_quotas[p as keyof DefaultPlatformQuotasMap]!.daily" type="number" step="0.01" min="0" :placeholder="t('admin.settings.platformQuota.placeholder')" size="sm" class="w-28" />
                </template>
                <template #weekly-cell="{ row: { original: { platform: p } } }">
                  <UInput v-model.number="form.default_platform_quotas[p as keyof DefaultPlatformQuotasMap]!.weekly" type="number" step="0.01" min="0" :placeholder="t('admin.settings.platformQuota.placeholder')" size="sm" class="w-28" />
                </template>
                <template #monthly-cell="{ row: { original: { platform: p } } }">
                  <UInput v-model.number="form.default_platform_quotas[p as keyof DefaultPlatformQuotasMap]!.monthly" type="number" step="0.01" min="0" :placeholder="t('admin.settings.platformQuota.placeholder')" size="sm" class="w-28" />
                </template>
              </UTable>
              <p class="text-xs text-warning mt-2">
                {{ t("admin.settings.defaults.platformQuotaNotice") }}
              </p>
            </UFormField>
            <!-- /全局平台限额矩阵 -->
          </SettingsSection>

          <SettingsSection
            :title="t('admin.settings.authSourceDefaults.title')"
            :description="t('admin.settings.authSourceDefaults.description')"
          >
            <UFormField
              :label="t('admin.settings.authSourceDefaults.requireEmailLabel')"
              :description="t('admin.settings.authSourceDefaults.requireEmailHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.force_email_on_third_party_signup" />
            </UFormField>
            <div
              v-for="authSource in authSourceDefaultsMeta"
              :key="authSource.source"
              class="border-t border-default pt-4 first:border-t-0 first:pt-0"
            >
              <div class="flex items-center justify-between gap-4">
                <div>
                  <div class="font-medium text-highlighted">
                    {{ authSource.title }}
                  </div>
                  <p class="mt-1 text-sm text-muted">
                    {{ authSource.description }}
                  </p>
                </div>
                <Toggle
                  v-model="
                    authSourceDefaults[authSource.source].grant_on_signup
                  "
                  :data-testid="`auth-source-${authSource.source}-enabled`"
                />
              </div>

              <div
                v-if="authSourceDefaults[authSource.source].grant_on_signup"
                :data-testid="`auth-source-${authSource.source}-panel`"
                class="mt-4 space-y-4 border-t border-default pt-4"
              >
                <p class="text-sm text-muted">
                  {{ t("admin.settings.authSourceDefaults.enabledHint") }}
                </p>

                <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
                  <UFormField :label="t('admin.settings.defaults.defaultBalance')">
                    <UInput
                      v-model.number="
                        authSourceDefaults[authSource.source].balance
                      "
                      type="number"
                      step="0.01"
                      min="0"
                      placeholder="0.00"
                      class="w-full"
                    />
                  </UFormField>
                  <UFormField :label="t('admin.settings.defaults.defaultConcurrency')">
                    <UInput
                      v-model.number="
                        authSourceDefaults[authSource.source].concurrency
                      "
                      type="number"
                      min="1"
                      placeholder="5"
                      class="w-full"
                    />
                  </UFormField>
                </div>

                <UFormField
                  :label="t('admin.settings.authSourceDefaults.grantOnFirstBindLabel')"
                  :description="t('admin.settings.authSourceDefaults.grantOnFirstBindHint')"
                  class="flex items-center justify-between gap-4"
                >
                  <Toggle
                    v-model="
                      authSourceDefaults[authSource.source]
                        .grant_on_first_bind
                    "
                  />
                </UFormField>

                <UFormField
                  :label="t('admin.settings.authSourceDefaults.defaultSubscriptionsLabel')"
                  :description="t('admin.settings.authSourceDefaults.defaultSubscriptionsHint')"
                  class="flex max-sm:flex-col justify-between sm:items-center gap-4"
                >
                  <UButton
                    @click="
                      addAuthSourceDefaultSubscription(authSource.source)
                    "
                    :disabled="subscriptionGroups.length === 0"
                    color="neutral"
                    variant="outline"
                    size="sm"
                  >
                    {{ t("admin.settings.defaults.addDefaultSubscription") }}
                  </UButton>
                </UFormField>

                <div
                  v-if="
                    authSourceDefaults[authSource.source].subscriptions
                      .length === 0
                  "
                  class="py-2 text-sm text-muted"
                >
                  {{ t("admin.settings.authSourceDefaults.noSourceSubscriptions") }}
                </div>

                <div v-else class="space-y-3">
                  <div
                    v-for="(item, index) in authSourceDefaults[
                      authSource.source
                    ].subscriptions"
                    :key="`${authSource.source}-sub-${index}`"
                    class="grid grid-cols-1 gap-3 md:grid-cols-[1fr_160px_auto] border-t border-default pt-4 first:border-t-0 first:pt-0"
                  >
                    <UFormField :label="t('admin.settings.defaults.subscriptionGroup')">
                      <Select
                        v-model="item.group_id"
                        class="default-sub-group-select"
                        :options="defaultSubscriptionGroupOptions"
                        :placeholder="
                          t('admin.settings.defaults.subscriptionGroup')
                        "
                      >
                        <template #selected="{ option }">
                          <GroupBadge
                            v-if="option"
                            :name="
                              (
                                option as unknown as DefaultSubscriptionGroupOption
                              ).label
                            "
                            :platform="
                              (
                                option as unknown as DefaultSubscriptionGroupOption
                              ).platform
                            "
                            :subscription-type="
                              (
                                option as unknown as DefaultSubscriptionGroupOption
                              ).subscriptionType
                            "
                            :rate-multiplier="
                              (
                                option as unknown as DefaultSubscriptionGroupOption
                              ).rate
                            "
                          />
                          <span v-else class="text-dimmed">
                            {{ t("admin.settings.defaults.subscriptionGroup") }}
                          </span>
                        </template>
                        <template #option="{ option, selected }">
                          <GroupOptionItem
                            :name="
                              (
                                option as unknown as DefaultSubscriptionGroupOption
                              ).label
                            "
                            :platform="
                              (
                                option as unknown as DefaultSubscriptionGroupOption
                              ).platform
                            "
                            :subscription-type="
                              (
                                option as unknown as DefaultSubscriptionGroupOption
                              ).subscriptionType
                            "
                            :rate-multiplier="
                              (
                                option as unknown as DefaultSubscriptionGroupOption
                              ).rate
                            "
                            :description="
                              (
                                option as unknown as DefaultSubscriptionGroupOption
                              ).description
                            "
                            :selected="selected"
                          />
                        </template>
                      </Select>
                    </UFormField>
                    <UFormField
                      :label="t(
                            'admin.settings.defaults.subscriptionValidityDays',
                          )"
                    >
                      <UInput v-model.number="item.validity_days" type="number" min="1" max="36500" class="w-full" />
                    </UFormField>
                    <div class="flex items-end">
                      <UButton
                        @click="
                          removeAuthSourceDefaultSubscription(
                            authSource.source,
                            index,
                          )
                        "
                        color="error"
                        variant="soft"
                        class="w-full"
                      >
                        {{ t("common.delete") }}
                      </UButton>
                    </div>
                  </div>
                </div>

                <!-- ★ 新增：auth source 平台限额覆盖区块 -->
                <UFormField
                  :label="t('admin.settings.authSourceDefaults.platformQuotasOverride')"
                  :description="t('admin.settings.authSourceDefaults.platformQuotasOverrideHint')"
                >
                  <UTable
                    :data="platformQuotaRows"
                    :columns="platformQuotaColumns"
                    :ui="{ th: 'px-2 py-2 text-xs', td: 'px-2 py-1.5' }"
                  >
                    <template #platform-cell="{ row: { original: { platform: p } } }">
                      <span class="font-mono text-xs text-toned">{{ p }}</span>
                    </template>
                    <template #daily-cell="{ row: { original: { platform: p } } }">
                      <UInput v-model.number="authSourceDefaults[authSource.source].platform_quotas[p as keyof DefaultPlatformQuotasMap]!.daily" type="number" step="0.01" min="0" :placeholder="t('admin.settings.platformQuota.placeholder')" size="sm" class="w-28" />
                    </template>
                    <template #weekly-cell="{ row: { original: { platform: p } } }">
                      <UInput v-model.number="authSourceDefaults[authSource.source].platform_quotas[p as keyof DefaultPlatformQuotasMap]!.weekly" type="number" step="0.01" min="0" :placeholder="t('admin.settings.platformQuota.placeholder')" size="sm" class="w-28" />
                    </template>
                    <template #monthly-cell="{ row: { original: { platform: p } } }">
                      <UInput v-model.number="authSourceDefaults[authSource.source].platform_quotas[p as keyof DefaultPlatformQuotasMap]!.monthly" type="number" step="0.01" min="0" :placeholder="t('admin.settings.platformQuota.placeholder')" size="sm" class="w-28" />
                    </template>
                  </UTable>
                </UFormField>
                <!-- /auth source 平台限额覆盖区块 -->
              </div>
            </div>
          </SettingsSection>
        </div>
        <!-- /Tab: Users -->

        <!-- Tab: Gateway — Claude Code, Scheduling -->
        <div v-show="activeTab === 'gateway'" class="flex flex-col gap-4 sm:gap-6 lg:gap-12">
          <!-- Claude Code Settings -->
          <SettingsSection
            :title="t('admin.settings.claudeCode.title')"
            :description="t('admin.settings.claudeCode.description')"
          >
            <UFormField
              :label="t('admin.settings.claudeCode.minVersion')"
              :description="t('admin.settings.claudeCode.minVersionHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
              :ui="{ container: 'w-full sm:max-w-sm' }"
            >
              <UInput
                v-model="form.min_claude_code_version"
                type="text"
                :placeholder="
                  t('admin.settings.claudeCode.minVersionPlaceholder')
                "
                class="max-w-xs font-mono"
              />
            </UFormField>
            <UFormField
              :label="t('admin.settings.claudeCode.maxVersion')"
              :description="t('admin.settings.claudeCode.maxVersionHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
              :ui="{ container: 'w-full sm:max-w-sm' }"
            >
              <UInput
                v-model="form.max_claude_code_version"
                type="text"
                :placeholder="
                  t('admin.settings.claudeCode.maxVersionPlaceholder')
                "
                class="max-w-xs font-mono"
              />
            </UFormField>
          </SettingsSection>

          <!-- Codex Settings -->
          <SettingsSection :title="t('admin.settings.gatewayForwarding.codexHardeningTitle')">
            <UFormField
              :label="t('admin.settings.gatewayForwarding.codexOverdraftTitle')"
              :description="t('admin.settings.gatewayForwarding.codexOverdraftDescription')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.codex_quota_overdraft_enabled" data-testid="codex-quota-overdraft-toggle" />
            </UFormField>
            <UFormField
              :label="t('admin.settings.gatewayForwarding.codexOverdraftBusinessInjection')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle
                v-model="form.codex_quota_overdraft_business_injection_enabled"
                :disabled="!form.codex_quota_overdraft_enabled"
                data-testid="codex-quota-overdraft-business-toggle"
              />
              <p class="text-xs text-warning mt-2">
                {{ t("admin.settings.gatewayForwarding.codexOverdraftBusinessInjectionHint") }}
              </p>
            </UFormField>
            <UFormField
              :label="t('admin.settings.gatewayForwarding.codexTicketEnabled')"
              :description="t('admin.settings.gatewayForwarding.codexTicketEnabledDesc')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle id="codex-ticket-enabled" v-model="form.openai_codex_ticket_enabled" />
            </UFormField>
            <h3 class="text-base font-semibold text-highlighted">
              {{ t("admin.settings.gatewayForwarding.codexTicketHarvestProxy") }}
            </h3>
            <p class="text-sm text-muted">
              {{ t("admin.settings.gatewayForwarding.codexTicketHarvestProxyDesc") }}
            </p>
            <UInput
              id="codex-ticket-harvest-proxy"
              v-model="form.openai_codex_ticket_harvest_proxy_url"
              type="text"
              :placeholder="t('admin.settings.gatewayForwarding.codexTicketHarvestProxyPlaceholder')"
              autocomplete="off"
              class="mt-3 w-full font-mono"
            />
            <p v-if="form.openai_codex_ticket_harvest_proxy_configured" class="text-xs text-muted">
              {{ t("admin.settings.gatewayForwarding.codexTicketHarvestProxyConfigured") }}
            </p>
            <h3 class="text-base font-semibold text-highlighted">
              {{ t("admin.settings.gatewayForwarding.codexClientRestrictionTitle") }}
            </h3>
            <p class="text-sm text-muted">
              {{ t("admin.settings.gatewayForwarding.codexHardeningDesc") }}
            </p>
            <div class="grid gap-4 sm:grid-cols-2">
              <UFormField :label="t('admin.settings.gatewayForwarding.minCodexVersion')">
                <UInput
                  v-model="form.min_codex_version"
                  type="text"
                  :placeholder="
                    t(
                      'admin.settings.gatewayForwarding.minCodexVersionPlaceholder',
                    )
                  "
                  class="w-full font-mono"
                />
              </UFormField>
              <UFormField :label="t('admin.settings.gatewayForwarding.maxCodexVersion')">
                <UInput
                  v-model="form.max_codex_version"
                  type="text"
                  :placeholder="
                    t(
                      'admin.settings.gatewayForwarding.maxCodexVersionPlaceholder',
                    )
                  "
                  class="w-full font-mono"
                />
              </UFormField>
            </div>
            <p class="text-xs text-muted">
              {{ t("admin.settings.gatewayForwarding.codexVersionHint") }}
            </p>
            <UFormField
              :label="t('admin.settings.gatewayForwarding.codexFingerprintSignals')"
              :description="t('admin.settings.gatewayForwarding.codexFingerprintSignalsDesc')"
              :ui="{ container: 'w-full mt-3' }"
            >
              <div
                v-for="(row, i) in codexFingerprintRows"
                :key="`codex-fp-${i}`"
                class="mb-2 flex items-center gap-2"
              >
                <Select
                  v-model="row.type"
                  :options="[{ value: 'header_exact', label: t('admin.settings.gatewayForwarding.codexFpTypeHeaderExact') }, { value: 'header_prefix', label: t('admin.settings.gatewayForwarding.codexFpTypeHeaderPrefix') }, { value: 'body_path', label: t('admin.settings.gatewayForwarding.codexFpTypeBodyPath') }]"
                  class="w-32"
                />
                <UInput
                  v-model="row.match"
                  type="text"
                  :placeholder="t('admin.settings.gatewayForwarding.codexFpMatchPlaceholder')"
                  class="flex-1 font-mono"
                />
                <label class="flex shrink-0 cursor-pointer items-center gap-2 text-sm text-toned">
                  <Toggle v-model="row.required" />
                  {{ t('admin.settings.gatewayForwarding.codexFpRequired') }}
                </label>
                <UButton
                  @click="removeCodexFingerprintRow(i)"
                  color="error"
                  variant="soft"
                  size="sm"
                  class="shrink-0"
                >
                  {{ t("admin.settings.gatewayForwarding.codexRemoveRow") }}
                </UButton>
              </div>
              <UButton @click="addCodexFingerprintRow" color="neutral" variant="outline" size="sm">
                {{ t("admin.settings.gatewayForwarding.codexAddRow") }}
              </UButton>
              <p v-if="codexFingerprintNoRequired" class="text-xs text-warning mt-2">
                {{ t("admin.settings.gatewayForwarding.codexFingerprintNoRequiredWarn") }}
              </p>
            </UFormField>
            <UFormField
              :label="t('admin.settings.gatewayForwarding.codexAllowAppServer')"
              :description="t(
                      'admin.settings.gatewayForwarding.codexAllowAppServerDesc',
                    )"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.codex_cli_only_allow_app_server_clients" />
            </UFormField>
            <UFormField
              :label="t('admin.settings.gatewayForwarding.codexBlacklist')"
              :description="t('admin.settings.gatewayForwarding.codexBlacklistDesc')"
              :ui="{ container: 'w-full mt-3' }"
            >
              <div v-for="(row, i) in codexBlacklistRows" :key="`codex-bl-${i}`" class="mb-2 flex gap-2">
                <UInput
                  v-model="row.originator"
                  type="text"
                  :placeholder="
                    t(
                      'admin.settings.gatewayForwarding.codexOriginatorPlaceholder',
                    )
                  "
                  class="w-1/3 font-mono"
                />
                <UInput
                  v-model="row.uaContains"
                  type="text"
                  :placeholder="
                    t(
                      'admin.settings.gatewayForwarding.codexUaContainsPlaceholder',
                    )
                  "
                  class="flex-1 font-mono"
                />
                <UButton @click="removeCodexBlacklistRow(i)" color="error" variant="soft" size="sm" class="shrink-0">
                  {{ t("admin.settings.gatewayForwarding.codexRemoveRow") }}
                </UButton>
              </div>
              <UButton @click="addCodexBlacklistRow" color="neutral" variant="outline" size="sm">
                {{ t("admin.settings.gatewayForwarding.codexAddRow") }}
              </UButton>
            </UFormField>
            <UFormField
              :label="t('admin.settings.gatewayForwarding.codexWhitelist')"
              :description="t('admin.settings.gatewayForwarding.codexWhitelistDesc')"
              :ui="{ container: 'w-full mt-3' }"
            >
              <div v-for="(row, i) in codexWhitelistRows" :key="`codex-wl-${i}`" class="mb-2 flex gap-2">
                <UInput
                  v-model="row.originator"
                  type="text"
                  :placeholder="
                    t(
                      'admin.settings.gatewayForwarding.codexOriginatorPlaceholder',
                    )
                  "
                  class="w-1/3 font-mono"
                />
                <UInput
                  v-model="row.uaContains"
                  type="text"
                  :placeholder="
                    t(
                      'admin.settings.gatewayForwarding.codexUaContainsPlaceholder',
                    )
                  "
                  class="flex-1 font-mono"
                />
                <label
                  class="flex shrink-0 cursor-pointer items-center gap-2 text-xs text-toned"
                  :title="t('admin.settings.gatewayForwarding.codexWhitelistSkipFingerprintTooltip')"
                >
                  <Toggle :model-value="row.skipEngineFingerprint ?? false" @update:model-value="row.skipEngineFingerprint = $event" />
                  {{ t('admin.settings.gatewayForwarding.codexWhitelistSkipFingerprint') }}
                </label>
                <UButton @click="removeCodexWhitelistRow(i)" color="error" variant="soft" size="sm" class="shrink-0">
                  {{ t("admin.settings.gatewayForwarding.codexRemoveRow") }}
                </UButton>
              </div>
              <UButton @click="addCodexWhitelistRow" color="neutral" variant="outline" size="sm">
                {{ t("admin.settings.gatewayForwarding.codexAddRow") }}
              </UButton>
            </UFormField>
          </SettingsSection>

          <!-- Upstream Billing Probe Settings -->
          <SettingsSection
            data-testid="upstream-billing-probe-settings"
            :title="t('admin.settings.upstreamBillingProbe.title')"
            :description="t('admin.settings.upstreamBillingProbe.description')"
            :loading="upstreamBillingProbeLoading"
          >
            <template #actions>
              <UButton
                :disabled="upstreamBillingProbeSaving"
                data-testid="upstream-billing-probe-save"
                @click="saveUpstreamBillingProbeSettings"
              >
                {{ upstreamBillingProbeSaving ? t("common.saving") : t("common.save") }}
              </UButton>
            </template>
            <UFormField
              :label="t('admin.settings.upstreamBillingProbe.enabled')"
              :description="t('admin.settings.upstreamBillingProbe.enabledHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle
                v-model="upstreamBillingProbeForm.enabled"
                :aria-label="t('admin.settings.upstreamBillingProbe.enabled')"
                data-testid="upstream-billing-probe-enabled"
              />
            </UFormField>
            <UFormField
              v-if="upstreamBillingProbeForm.enabled"
              :label="t('admin.settings.upstreamBillingProbe.intervalMinutes')"
              :description="t('admin.settings.upstreamBillingProbe.intervalHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
            >
              <UInput
                id="upstream-billing-probe-interval"
                v-model.number="upstreamBillingProbeForm.interval_minutes"
                type="number"
                min="5"
                max="1440"
                data-testid="upstream-billing-probe-interval"
                @keydown.enter.prevent="saveUpstreamBillingProbeSettings"
                class="w-32"
              />
            </UFormField>
          </SettingsSection>

          <!-- Ollama Cloud Usage Settings -->
          <SettingsSection
            data-testid="ollama-cloud-usage-global-settings"
            :title="t('admin.settings.ollamaCloudUsage.title')"
            :description="t('admin.settings.ollamaCloudUsage.description')"
            :loading="ollamaCloudUsageLoading"
          >
            <template #actions>
              <UButton
                :disabled="ollamaCloudUsageSaving"
                data-testid="ollama-cloud-usage-global-save"
                @click="saveOllamaCloudUsageSettings"
              >
                {{ ollamaCloudUsageSaving ? t("common.saving") : t("common.save") }}
              </UButton>
            </template>
            <UFormField
              :label="t('admin.settings.ollamaCloudUsage.enabled')"
              :description="t('admin.settings.ollamaCloudUsage.enabledHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle
                v-model="ollamaCloudUsageForm.enabled"
                :aria-label="t('admin.settings.ollamaCloudUsage.enabled')"
                data-testid="ollama-cloud-usage-global-enabled"
              />
            </UFormField>
            <template v-if="ollamaCloudUsageForm.enabled">
              <UFormField
                :label="t('admin.settings.ollamaCloudUsage.debounceMinutes')"
                :description="t('admin.settings.ollamaCloudUsage.debounceHint')"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <UInput
                  id="ollama-cloud-usage-debounce"
                  v-model.number="ollamaCloudUsageForm.debounce_minutes"
                  type="number"
                  min="1"
                  max="60"
                  data-testid="ollama-cloud-usage-global-debounce"
                  @keydown.enter.prevent="saveOllamaCloudUsageSettings"
                  class="w-32"
                />
              </UFormField>
              <UFormField
                :label="t('admin.settings.ollamaCloudUsage.intervalMinutes')"
                :description="t('admin.settings.ollamaCloudUsage.intervalHint')"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <UInput
                  id="ollama-cloud-usage-interval"
                  v-model.number="ollamaCloudUsageForm.interval_minutes"
                  type="number"
                  min="15"
                  max="1440"
                  data-testid="ollama-cloud-usage-global-interval"
                  @keydown.enter.prevent="saveOllamaCloudUsageSettings"
                  class="w-32"
                />
              </UFormField>
            </template>
          </SettingsSection>
          <!-- Gateway Scheduling Settings -->
          <SettingsSection
            :title="t('admin.settings.scheduling.title')"
            :description="t('admin.settings.scheduling.description')"
          >
            <UFormField
              :label="t('admin.settings.scheduling.allowUngroupedKey')"
              :description="t('admin.settings.scheduling.allowUngroupedKeyHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.allow_ungrouped_key_scheduling" />
            </UFormField>
            <UFormField
              :label="t(
                      'admin.settings.scheduling.accountSchedulingThresholdsTitle',
                    )"
              :ui="{ container: 'w-full mt-3' }"
            >
              <template #description>
                <span class="block">
                  {{ t( "admin.settings.scheduling.accountSchedulingThresholdsDescription", ) }}
                </span>
                <span class="block mt-1">
                  {{ t( "admin.settings.scheduling.accountSchedulingThresholdsGlobalHint", ) }}
                </span>
              </template>
              <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
                <div
                  v-for="platform in schedulingThresholdPlatforms"
                  :key="platform"
                  class="border-t border-default pt-4 first:border-t-0 first:pt-0"
                >
                  <UFormField
                    :label="platform"
                    :description="t(
                            'admin.settings.scheduling.accountSchedulingThresholdsRangeHint',
                          )"
                  >
                    <span class="rounded-sm bg-elevated px-2 py-0.5 text-[11px] font-medium text-toned">
                      %
                    </span>
                  </UFormField>
                  <UInput
                    v-model.number="form.account_scheduling_thresholds[platform]"
                    type="number"
                    min="1"
                    max="100"
                    step="1"
                    :data-testid="`account-scheduling-threshold-${platform}`"
                    placeholder="100"
                    class="w-full mt-3"
                  />
                </div>
              </div>
              <p class="text-xs text-warning mt-2">
                {{ t( "admin.settings.scheduling.accountSchedulingThresholdsDisabledHint", ) }}
              </p>
            </UFormField>
            <UFormField
              v-if="!form.openai_advanced_scheduler_enabled"
              :label="t('admin.settings.openaiExperimentalScheduler.lowRatePriorityTitle')"
              :description="t('admin.settings.openaiExperimentalScheduler.lowRatePriorityDescription')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle
                v-model="form.openai_low_upstream_rate_priority_enabled"
                data-testid="openai-low-rate-priority-toggle"
              />
            </UFormField>
            <UFormField
              v-if="!form.openai_advanced_scheduler_enabled && form.openai_low_upstream_rate_priority_enabled"
              :label="t('admin.settings.openaiExperimentalScheduler.oauthRateTitle')"
              :description="t('admin.settings.openaiExperimentalScheduler.oauthRatePriorityDescription')"
              class="flex max-sm:flex-col justify-between sm:items-center gap-4"
            >
              <div class="relative w-full shrink-0 sm:w-32">
                <UInput
                  id="openai-oauth-scheduling-rate-multiplier"
                  v-model.number="form.openai_oauth_scheduling_rate_multiplier"
                  data-testid="openai-oauth-scheduling-rate-multiplier"
                  min="0"
                  required
                  step="0.01"
                  type="number"
                  class="w-full"
                />
                <span class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-sm text-dimmed">x</span>
              </div>
            </UFormField>
            <UFormField
              :label="t('admin.settings.openaiExperimentalScheduler.title')"
              :description="t('admin.settings.openaiExperimentalScheduler.description')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.openai_advanced_scheduler_enabled" data-testid="openai-advanced-scheduler-toggle" />
            </UFormField>
            <UFormField
              v-if="form.openai_advanced_scheduler_enabled"
              :label="t('admin.settings.openaiExperimentalScheduler.stickyWeightedTitle')"
              :description="t('admin.settings.openaiExperimentalScheduler.stickyWeightedDescription')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.openai_advanced_scheduler_sticky_weighted_enabled" />
            </UFormField>
            <UFormField
              v-if="form.openai_advanced_scheduler_enabled"
              :label="t('admin.settings.openaiExperimentalScheduler.subscriptionPriorityTitle')"
              :description="t('admin.settings.openaiExperimentalScheduler.subscriptionPriorityDescription')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.openai_advanced_scheduler_subscription_priority_enabled" />
            </UFormField>
            <UFormField
              v-if="form.openai_advanced_scheduler_enabled"
              :label="t('admin.settings.openaiExperimentalScheduler.oauthRateTitle')"
              :description="t('admin.settings.openaiExperimentalScheduler.oauthRateWeightedDescription')"
              class="flex max-sm:flex-col justify-between sm:items-center gap-4"
            >
              <div class="relative w-full shrink-0 sm:w-32">
                <UInput
                  id="openai-oauth-scheduling-rate-multiplier"
                  v-model.number="form.openai_oauth_scheduling_rate_multiplier"
                  data-testid="openai-oauth-scheduling-rate-multiplier"
                  min="0"
                  required
                  step="0.01"
                  type="number"
                  class="w-full"
                />
                <span class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-sm text-dimmed">x</span>
              </div>
            </UFormField>
            <UFormField
              v-if="form.openai_advanced_scheduler_enabled"
              :label="t('admin.settings.openaiExperimentalScheduler.weightsTitle')"
              :description="t('admin.settings.openaiExperimentalScheduler.weightsDescription')"
              :ui="{ container: 'w-full mt-3' }"
            >
              <div class="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-5">
                <label v-for="field in openAIAdvancedSchedulerWeightFields" :key="field.key" class="block">
                  <span class="text-xs font-medium text-toned">
                    {{ field.label }}
                  </span>
                  <UInput
                    v-model="form[field.key]"
                    inputmode="decimal"
                    :placeholder="field.placeholder"
                    type="text"
                    class="w-full mt-1"
                  />
                </label>
              </div>
            </UFormField>
          </SettingsSection>

          <!-- Gateway Forwarding Behavior -->
          <SettingsSection
            id="settings-gateway-forwarding"
            :title="t('admin.settings.gatewayForwarding.title')"
            :description="t('admin.settings.gatewayForwarding.description')"
          >
            <div class="grid gap-5 border-b border-default pb-5 md:grid-cols-[minmax(0,1fr)_auto] md:items-end">
              <UFormField
                :label="t('admin.settings.gatewayForwarding.grokDefaultTextModel')"
                :description="t('admin.settings.gatewayForwarding.grokDefaultTextModelHint')"
              >
                <UInput
                  id="grok-default-text-model"
                  v-model.trim="form.grok_default_text_model"
                  type="text"
                  list="grok-default-text-model-options"
                  data-testid="grok-default-text-model"
                  placeholder="grok-4.5"
                  class="mt-2 w-full"
                />
                <datalist id="grok-default-text-model-options">
                  <option value="grok-4.5" />
                  <option value="grok-4.1-fast" />
                  <option value="grok-4" />
                </datalist>
              </UFormField>
              <UFormField
                :label="t('admin.settings.gatewayForwarding.grokCrossClientMap')"
                :description="t('admin.settings.gatewayForwarding.grokCrossClientMapHint')"
                class="flex items-center justify-between gap-4"
              >
                <Toggle
                  v-model="form.grok_cross_client_model_map_enabled"
                  data-testid="grok-cross-client-model-map-toggle"
                />
              </UFormField>
              </div>
            <UFormField
              :label="t('admin.settings.gatewayForwarding.grokDefaultBaseURLMode')"
              :description="t('admin.settings.gatewayForwarding.grokDefaultBaseURLModeHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4 md:col-span-2"
            >
              <Select
                id="grok-default-base-url-mode"
                v-model="form.grok_default_base_url_mode"
                data-testid="grok-default-base-url-mode"
                :options="[{ value: 'cli', label: t('admin.settings.gatewayForwarding.grokBaseURLModeCLI') }, { value: 'api', label: t('admin.settings.gatewayForwarding.grokBaseURLModeAPI') }, { value: 'us-east-1', label: t('admin.settings.gatewayForwarding.grokBaseURLModeUSEast1') }, { value: 'us-west-2', label: t('admin.settings.gatewayForwarding.grokBaseURLModeUSWest2') }, { value: 'eu-west-1', label: t('admin.settings.gatewayForwarding.grokBaseURLModeEUWest1') }]"
                class="mt-2 w-full"
              />
            </UFormField>
            <!-- OpenAI Responses 首 token 统计 -->
            <UFormField
              :label="t('admin.settings.gatewayForwarding.openaiTTFTMode')"
              :description="t('admin.settings.gatewayForwarding.openaiTTFTModeHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4 md:col-span-2"
            >
              <Select
                id="openai-ttft-mode"
                v-model="form.openai_ttft_mode"
                data-testid="openai-ttft-mode"
                :options="[{ value: 'semantic', label: t('admin.settings.gatewayForwarding.openaiTTFTModeSemantic') }, { value: 'visible', label: t('admin.settings.gatewayForwarding.openaiTTFTModeVisible') }]"
                class="mt-2 w-full"
              />
            </UFormField>
            <!-- Fingerprint Unification -->
            <UFormField
              :label="t(
                      'admin.settings.gatewayForwarding.fingerprintUnification',
                    )"
              :description="t(
                      'admin.settings.gatewayForwarding.fingerprintUnificationHint',
                    )"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.enable_fingerprint_unification" />
            </UFormField>
            <!-- Metadata Passthrough -->
            <UFormField
              :label="t('admin.settings.gatewayForwarding.metadataPassthrough')"
              :description="t(
                      'admin.settings.gatewayForwarding.metadataPassthroughHint',
                    )"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.enable_metadata_passthrough" />
            </UFormField>
            <!-- CCH Signing -->
            <UFormField
              :label="t('admin.settings.gatewayForwarding.cchSigning')"
              :description="t('admin.settings.gatewayForwarding.cchSigningHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.enable_cch_signing" />
            </UFormField>
            <!-- Claude OAuth System Prompt Injection -->
            <UFormField
              :label="t(
                      'admin.settings.gatewayForwarding.claudeOAuthSystemPromptInjection',
                    )"
              :description="t(
                      'admin.settings.gatewayForwarding.claudeOAuthSystemPromptInjectionHint',
                    )"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.enable_claude_oauth_system_prompt_injection" />
            </UFormField>
            <UFormField
              :label="t(
                    'admin.settings.gatewayForwarding.claudeOAuthSystemPromptBlocks',
                  )"
              :description="t(
                    'admin.settings.gatewayForwarding.claudeOAuthSystemPromptBlocksHint',
                  )"
              :ui="{ container: 'w-full mt-3' }"
            >
              <div class="space-y-3">
                <div
                  v-for="(block, index) in claudeOAuthSystemPromptBlocks"
                  :key="block.id"
                  class="border-t border-default pt-4 first:border-t-0 first:pt-0"
                >
                  <div
                    :class="[
                      'flex flex-wrap items-center justify-between gap-3',
                      block.expanded && 'mb-3',
                    ]"
                  >
                    <div class="min-w-0">
                      <div class="text-sm font-medium text-highlighted">
                        {{ t( "admin.settings.gatewayForwarding.systemBlockTitle", { index: index + 1 }, ) }}
                      </div>
                      <div class="mt-0.5 text-xs text-muted">
                        {{ getClaudeOAuthPresetLabel(block.preset) }}
                      </div>
                    </div>
                    <div class="flex items-center gap-2">
                      <UButton
                        :title="
                          block.expanded
                            ? t(
                                'admin.settings.gatewayForwarding.systemBlockHide',
                              )
                            : t(
                                'admin.settings.gatewayForwarding.systemBlockShow',
                              )
                        "
                        :aria-label="
                          block.expanded
                            ? t(
                                'admin.settings.gatewayForwarding.systemBlockHide',
                              )
                            : t(
                                'admin.settings.gatewayForwarding.systemBlockShow',
                              )
                        "
                        @click="toggleClaudeOAuthSystemPromptBlock(index)"
                        color="neutral"
                        variant="outline"
                        size="sm"
                      >
                        <UIcon :name="block.expanded ? 'i-lucide-eye-off' : 'i-lucide-eye'" class="size-3 shrink-0" />
                      </UButton>
                      <UButton
                        icon="i-lucide-arrow-up"
                        square
                        :disabled="index === 0"
                        @click="moveClaudeOAuthSystemPromptBlock(index, -1)"
                        color="neutral"
                        variant="outline"
                        size="sm"
                      />
                      <UButton
                        icon="i-lucide-arrow-down"
                        square
                        :disabled="
                          index === claudeOAuthSystemPromptBlocks.length - 1
                        "
                        @click="moveClaudeOAuthSystemPromptBlock(index, 1)"
                        color="neutral"
                        variant="outline"
                        size="sm"
                      />
                      <Toggle v-model="block.enabled" />
                      <UButton
                        icon="i-lucide-trash-2"
                        square
                        @click="removeClaudeOAuthSystemPromptBlock(index)"
                        color="error"
                        variant="soft"
                        size="sm"
                      />
                    </div>
                  </div>

                  <div v-show="block.expanded">
                    <div class="grid gap-3 md:grid-cols-2">
                      <UFormField
                        :label="t(
                              'admin.settings.gatewayForwarding.systemBlockPreset',
                            )"
                      >
                        <Select
                          v-model="block.preset"
                          :options="claudeOAuthSystemPromptPresetOptions"
                          @change="
                            (value) =>
                              applyClaudeOAuthSystemPromptPreset(index, value)
                          "
                        />
                      </UFormField>
                      <UFormField
                        :label="t(
                              'admin.settings.gatewayForwarding.systemBlockType',
                            )"
                      >
                        <Select v-model="block.type" :options="claudeOAuthSystemPromptBlockTypeOptions" />
                      </UFormField>
                    </div>

                    <UFormField :label="t('admin.settings.gatewayForwarding.systemBlockText')">
                      <UTextarea
                        v-model="block.text"
                        :rows="6"
                        @input="markClaudeOAuthSystemPromptBlockCustom(block)"
                        class="w-full font-mono"
                      />
                    </UFormField>

                    <div class="mt-3 grid gap-3 md:grid-cols-[minmax(0,1fr)_160px]">
                      <UFormField
                        :label="t(
                                'admin.settings.gatewayForwarding.systemBlockCacheControl',
                              )"
                        class="flex items-center justify-between gap-4"
                      >
                        <Toggle v-model="block.cacheControlEnabled" />
                      </UFormField>
                      <div v-if="block.cacheControlEnabled">
                        <Select v-model="block.cacheControlTTL" :options="claudeOAuthSystemPromptCacheTTLOptions" />
                      </div>
                    </div>
                  </div>
                </div>
              </div>
              <div class="mt-3 flex flex-wrap gap-2">
                <UButton
                  icon="i-lucide-plus"
                  @click="addClaudeOAuthSystemPromptBlock"
                  color="neutral"
                  variant="outline"
                  size="sm"
                >
                  {{ t("admin.settings.gatewayForwarding.addSystemBlock") }}
                </UButton>
                <UButton
                  icon="i-lucide-refresh-cw"
                  @click="resetClaudeOAuthSystemPromptBlocks"
                  color="neutral"
                  variant="outline"
                  size="sm"
                >
                  {{ t("admin.settings.gatewayForwarding.resetSystemBlocks") }}
                </UButton>
              </div>
            </UFormField>
            <!-- Anthropic Cache TTL 1h Injection -->
            <UFormField
              :label="t(
                      'admin.settings.gatewayForwarding.anthropicCacheTTL1hInjection',
                    )"
              :description="t(
                      'admin.settings.gatewayForwarding.anthropicCacheTTL1hInjectionHint',
                    )"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.enable_anthropic_cache_ttl_1h_injection" />
            </UFormField>
            <!-- 预扣费系统运行时开关：两套互相独立的提前占钱机制，默认都关 -->
            <UFormField
              :label="t(
                      'admin.settings.gatewayForwarding.balancePreauthorizationEnabled',
                    )"
              :description="t(
                      'admin.settings.gatewayForwarding.balancePreauthorizationEnabledHint',
                    )"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.billing_balance_preauthorization_enabled" />
            </UFormField>
            <UFormField
              :label="t(
                      'admin.settings.gatewayForwarding.inflightReservationEnabled',
                    )"
              :description="t(
                      'admin.settings.gatewayForwarding.inflightReservationEnabledHint',
                    )"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.billing_inflight_reservation_enabled" />
            </UFormField>
            <!-- messages cache_control 改写 -->
            <UFormField
              :label="t(
                      'admin.settings.gatewayForwarding.rewriteMessageCacheControl',
                    )"
              :description="t(
                      'admin.settings.gatewayForwarding.rewriteMessageCacheControlHint',
                    )"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.rewrite_message_cache_control" />
            </UFormField>
            <!-- 客户端 dateline 归一化（仅 Anthropic OAuth/SetupToken） -->
            <UFormField
              :label="t(
                      'admin.settings.gatewayForwarding.clientDatelineNormalization',
                    )"
              :description="t(
                      'admin.settings.gatewayForwarding.clientDatelineNormalizationHint',
                    )"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.enable_client_dateline_normalization" />
            </UFormField>
            <!-- Antigravity UA 版本 -->
            <UFormField
              :label="t(
                    'admin.settings.gatewayForwarding.antigravityUserAgentVersion',
                  )"
              :description="t(
                    'admin.settings.gatewayForwarding.antigravityUserAgentVersionHint',
                  )"
              class="flex max-sm:flex-col justify-between items-start gap-4"
              :ui="{ container: 'w-full sm:max-w-sm' }"
            >
              <UInput
                v-model="form.antigravity_user_agent_version"
                type="text"
                :placeholder="
                  t(
                    'admin.settings.gatewayForwarding.antigravityUserAgentVersionPlaceholder',
                  )
                "
                class="max-w-xs font-mono"
              />
            </UFormField>
            <!-- OpenAI Codex UA -->
            <UFormField
              :label="t(
                    'admin.settings.gatewayForwarding.openaiCodexUserAgent',
                  )"
              :description="t(
                    'admin.settings.gatewayForwarding.openaiCodexUserAgentHint',
                  )"
              class="flex max-sm:flex-col justify-between items-start gap-4"
              :ui="{ container: 'w-full sm:max-w-sm' }"
            >
              <UInput
                v-model="form.openai_codex_user_agent"
                type="text"
                :placeholder="
                  t(
                    'admin.settings.gatewayForwarding.openaiCodexUserAgentPlaceholder',
                  )
                "
                class="w-full font-mono"
              />
            </UFormField>
            <!-- Codex 客户端版本号 -->
            <UFormField
              :label="t(
                    'admin.settings.gatewayForwarding.openaiCodexClientVersion',
                  )"
              :description="t(
                    'admin.settings.gatewayForwarding.openaiCodexClientVersionHint',
                  )"
              class="flex max-sm:flex-col justify-between items-start gap-4"
              :ui="{ container: 'w-full sm:max-w-sm' }"
            >
              <UInput
                v-model="form.openai_codex_client_version"
                type="text"
                :placeholder="
                  t(
                    'admin.settings.gatewayForwarding.openaiCodexClientVersionPlaceholder',
                  )
                "
                class="w-full font-mono"
              />
            </UFormField>
            <!-- Codex 版本号自动同步 -->
            <UFormField
              :label="t(
                      'admin.settings.gatewayForwarding.openaiCodexVersionAutoSync',
                    )"
              class="flex items-center justify-between gap-4"
            >
              <template #description>
                <span class="block">
                  {{ t( "admin.settings.gatewayForwarding.openaiCodexVersionAutoSyncHint", ) }}
                </span>
                <span v-if="codexSyncedVersionLabel" class="block mt-1">
                  {{ codexSyncedVersionLabel }}
                </span>
              </template>
              <Toggle v-model="form.openai_codex_version_auto_sync_enabled" />
            </UFormField>
          </SettingsSection>

          <!-- Web Search Emulation -->
          <SettingsSection
            :title="t('admin.settings.webSearchEmulation.title')"
            :description="t('admin.settings.webSearchEmulation.description')"
          >
            <!-- Global Toggle -->
            <UFormField
              :label="t('admin.settings.webSearchEmulation.enabled')"
              :description="t('admin.settings.webSearchEmulation.enabledHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="webSearchConfig.enabled" />
            </UFormField>
            <!-- Providers -->
            <template v-if="webSearchConfig.enabled">
              <UFormField
                :label="t('admin.settings.webSearchEmulation.providers')"
                class="flex max-sm:flex-col justify-between sm:items-center gap-4"
              >
                <UButton @click="addWebSearchProvider" color="neutral" variant="outline" size="sm">
                  {{ t("admin.settings.webSearchEmulation.addProvider") }}
                </UButton>
              </UFormField>
              <div
                v-if="webSearchConfig.providers.length === 0"
                class="py-2 text-center text-sm text-muted"
              >
                {{ t("admin.settings.webSearchEmulation.noProviders") }}
              </div>
              <div
                v-for="(provider, pIdx) in webSearchConfig.providers"
                :key="pIdx"
                class="rounded-lg border border-default"
              >
                <!-- Collapsible header -->
                <div
                  class="flex cursor-pointer items-center justify-between px-4 py-3"
                  @click="toggleProviderExpand(pIdx)"
                >
                  <div class="flex items-center gap-3">
                    <UIcon name="i-lucide-chevron-right" class="size-4" />
                    <Select
                      v-model="provider.type"
                      :options="[
                        { value: 'brave', label: 'Brave Search' },
                        { value: 'tavily', label: 'Tavily' },
                      ]"
                      class="w-36"
                      @click.stop
                    />
                    <!-- Quota summary (always visible) -->
                    <span class="text-xs text-dimmed">
                      {{ provider.quota_used ?? 0 }} /
                      {{ provider.quota_limit != null && provider.quota_limit > 0 ? provider.quota_limit : "∞" }}
                    </span>
                    <span
                      v-if="
                        !expandedProviders[pIdx] &&
                        provider.api_key_configured
                      "
                      class="text-xs text-success"
                    >
                      {{ t( "admin.settings.webSearchEmulation.apiKeyConfigured", ) }}
                    </span>
                  </div>
                  <UButton @click.stop="removeWebSearchProvider(pIdx)" color="error" variant="ghost" size="sm">
                    {{ t("admin.settings.webSearchEmulation.removeProvider") }}
                  </UButton>
                </div>

                <!-- Expanded content -->
                <div v-if="expandedProviders[pIdx]" class="space-y-3 border-t border-default px-4 pb-4 pt-3">
                  <!-- API Key with inline show/copy -->
                  <UFormField :label="t('admin.settings.webSearchEmulation.apiKey')">
                    <div class="relative">
                      <UInput
                        v-model="provider.api_key"
                        :type="apiKeyVisible[pIdx] ? 'text' : 'password'"
                        :class="
                          provider.api_key || provider.api_key_configured
                            ? 'pr-16'
                            : ''
                        "
                        :placeholder="
                          provider.api_key_configured
                            ? '••••••••'
                            : t(
                                'admin.settings.webSearchEmulation.apiKeyPlaceholder',
                              )
                        "
                        class="w-full"
                      />
                      <div
                        v-if="provider.api_key || provider.api_key_configured"
                        class="absolute inset-y-0 right-0 flex items-center pr-1.5"
                      >
                        <UButton
                          :title="
                            apiKeyVisible[pIdx]
                              ? t(
                                  'admin.settings.webSearchEmulation.hideApiKey',
                                )
                              : t(
                                  'admin.settings.webSearchEmulation.showApiKey',
                                )
                          "
                          @click="apiKeyVisible[pIdx] = !apiKeyVisible[pIdx]"
                          color="neutral"
                          variant="ghost"
                        >
                          <UIcon v-if="!apiKeyVisible[pIdx]" name="i-lucide-eye" class="size-4" />
                          <UIcon v-else name="i-lucide-eye-off" class="size-4" />
                        </UButton>
                        <UButton
                          icon="i-lucide-copy"
                          square
                          :aria-label="
                            t('admin.settings.webSearchEmulation.copyApiKey')
                          "
                          :title="
                            t('admin.settings.webSearchEmulation.copyApiKey')
                          "
                          :disabled="!provider.api_key"
                          @click="copyApiKey(pIdx)"
                          :class="{
                            'opacity-30 cursor-not-allowed':
                              !provider.api_key,
                          }"
                          color="neutral"
                          variant="ghost"
                        />
                      </div>
                    </div>
                  </UFormField>

                  <!-- Quota + Subscription in compact row -->
                  <div class="grid grid-cols-2 gap-3">
                    <UFormField
                      :label="t('admin.settings.webSearchEmulation.quotaLimit')"
                      :description="t(
                            'admin.settings.webSearchEmulation.quotaLimitHint',
                          )"
                    >
                      <UInput v-model="provider.quota_limit" type="number" min="1" :placeholder="'∞'" class="w-full" />
                    </UFormField>
                    <UFormField
                      :label="t('admin.settings.webSearchEmulation.subscribedAt')"
                      :description="t(
                            'admin.settings.webSearchEmulation.subscribedAtHint',
                          )"
                    >
                      <UInput
                        :value="formatSubscribedAt(provider.subscribed_at)"
                        type="date"
                        @input="
                          provider.subscribed_at = parseSubscribedAt(
                            ($event.target as HTMLInputElement).value,
                          )
                        "
                        class="w-full"
                      />
                    </UFormField>
                  </div>

                  <!-- Usage display -->
                  <div class="flex items-center gap-2">
                    <span class="text-xs text-muted">{{ t("admin.settings.webSearchEmulation.quotaUsage") }}:</span>
                    <div
                      v-if="
                        provider.quota_limit != null &&
                        provider.quota_limit > 0
                      "
                      class="flex-1 rounded-full bg-accented"
                      style="height: 6px"
                    >
                      <div
                        class="h-full rounded-full"
                        :class="
                          quotaPercentage(provider) > 90
                            ? 'bg-error'
                            : quotaPercentage(provider) > 70
                              ? 'bg-warning'
                              : 'bg-success'
                        "
                        :style="{
                          width:
                            Math.min(quotaPercentage(provider), 100) + '%',
                        }"
                      />
                    </div>
                    <div v-else class="flex-1" />
                    <span class="text-xs text-muted">{{ provider.quota_used ?? 0 }} /
                      {{ provider.quota_limit != null && provider.quota_limit > 0 ? provider.quota_limit : "∞" }}</span>
                    <UButton
                      v-if="(provider.quota_used ?? 0) > 0"
                      @click="resetWebSearchUsage(pIdx)"
                      variant="link"
                      size="sm"
                    >
                      {{ t("admin.settings.webSearchEmulation.resetUsage") }}
                    </UButton>
                  </div>

                  <!-- Proxy + Test on same row -->
                  <div class="flex items-end gap-3">
                    <UFormField :label="t('admin.settings.webSearchEmulation.proxy')">
                      <ProxySelector v-model="provider.proxy_id" :proxies="webSearchProxies" />
                    </UFormField>
                    <UButton @click="openTestDialog()" color="neutral" variant="outline" size="sm">
                      {{ t("admin.settings.webSearchEmulation.test") }}
                    </UButton>
                  </div>
                </div>
              </div>
            </template>
          </SettingsSection>

          <!-- Web Search Test Dialog -->
          <BaseDialog
            :show="wsTestDialogOpen"
            :title="t('admin.settings.webSearchEmulation.testResultTitle')"
            width="normal"
            @close="wsTestDialogOpen = false"
          >
            <div class="flex items-center gap-2">
              <UInput
                v-model="wsTestQuery"
                type="text"
                :placeholder="
                  t('admin.settings.webSearchEmulation.testDefaultQuery')
                "
                @keyup.enter="testWebSearchProvider()"
                class="flex-1"
              />
              <UButton :disabled="wsTestLoading" @click="testWebSearchProvider()" size="sm">
                {{
                  wsTestLoading
                    ? t("admin.settings.webSearchEmulation.testing")
                    : t("admin.settings.webSearchEmulation.test")
                }}
              </UButton>
            </div>
            <div v-if="wsTestResult" class="mt-4 max-h-80 overflow-y-auto rounded-lg bg-elevated p-4">
              <p class="mb-2 text-sm font-medium text-toned">
                {{ t("admin.settings.webSearchEmulation.testResultProvider") }}: {{ wsTestResult.provider }}
              </p>
              <div v-if="wsTestResult.results.length === 0" class="text-sm text-dimmed">
                {{ t("admin.settings.webSearchEmulation.testNoResults") }}
              </div>
              <div
                v-for="(r, rIdx) in wsTestResult.results"
                :key="rIdx"
                class="mt-2 border-t border-default pt-2 first:mt-0 first:border-0 first:pt-0"
              >
                <a :href="r.url" target="_blank" class="text-sm font-medium text-info hover:underline">{{ r.title }}</a>
                <p class="mt-0.5 text-xs text-muted">
                  {{ r.snippet }}
                </p>
              </div>
            </div>
            <template #footer>
              <div class="flex justify-end gap-3">
                <UButton @click="wsTestDialogOpen = false" color="neutral" variant="outline" size="sm">
                  {{ t("common.close") }}
                </UButton>
              </div>
            </template>
          </BaseDialog>

        <!-- Usage Records Settings -->
        <SettingsSection
          :title="t('admin.settings.usageRecords.title')"
          :description="t('admin.settings.usageRecords.description')"
        >
          <!-- User error requests visibility -->
          <UFormField
            :label="t('admin.settings.user_error_view.label')"
            :description="t('admin.settings.user_error_view.description')"
            class="flex items-center justify-between gap-4"
          >
            <Toggle v-model="form.allow_user_view_error_requests" />
          </UFormField>
        </SettingsSection>
        </div>
        <!-- /Tab: Gateway — Claude Code, Scheduling -->

        <!-- Tab: General -->
        <div v-show="activeTab === 'general'" class="flex flex-col gap-4 sm:gap-6 lg:gap-12">
          <!-- Site Settings -->
          <SettingsSection id="settings-site" :title="t('admin.settings.site.title')" :description="t('admin.settings.site.description')">
            <!-- Backend Mode -->
            <UFormField
              :label="t('admin.settings.site.backendMode')"
              :description="t('admin.settings.site.backendModeDescription')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.backend_mode_enabled" />
            </UFormField>
            <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
             <UFormField
               :label="t('admin.settings.site.siteName')"
               :description="t('admin.settings.site.siteNameHint')"
             >
               <UInput
                 v-model="form.site_name"
                 type="text"
                 :placeholder="t('admin.settings.site.siteNamePlaceholder')"
                 class="w-full"
               />
             </UFormField>
             <UFormField
               :label="t('admin.settings.site.siteSubtitle')"
               :description="t('admin.settings.site.siteSubtitleHint')"
             >
               <UInput
                 v-model="form.site_subtitle"
                 type="text"
                 :placeholder="
                   t('admin.settings.site.siteSubtitlePlaceholder')
                 "
                 class="w-full"
               />
             </UFormField>
           </div>
            <!-- API Base URL -->
            <UFormField
              :label="t('admin.settings.site.apiBaseUrl')"
              :description="t('admin.settings.site.apiBaseUrlHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
              :ui="{ container: 'w-full sm:max-w-sm' }"
            >
              <UInput
                v-model="form.api_base_url"
                type="text"
                :placeholder="t('admin.settings.site.apiBaseUrlPlaceholder')"
                class="w-full font-mono"
              />
            </UFormField>
            <!-- Global Table Preferences -->
            <UFormField
              :label="t('admin.settings.site.tablePreferencesTitle') + ' · ' + t('admin.settings.site.tableDefaultPageSize')"
              :help="t('admin.settings.site.tablePreferencesDescription')"
              :description="t('admin.settings.site.tableDefaultPageSizeHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
            >
              <UInput
                v-model.number="form.table_default_page_size"
                type="number"
                min="5"
                max="1000"
                step="1"
                class="w-40"
              />
            </UFormField>
            <UFormField
              :label="t('admin.settings.site.tablePreferencesTitle') + ' · ' + t('admin.settings.site.tablePageSizeOptions')"
              :description="t('admin.settings.site.tablePageSizeOptionsHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
              :ui="{ container: 'w-full sm:max-w-sm' }"
            >
              <UInput
                v-model="tablePageSizeOptionsInput"
                type="text"
                :placeholder="t('admin.settings.site.tablePageSizeOptionsPlaceholder')"
                class="w-full font-mono"
              />
            </UFormField>
            <!-- Custom Endpoints -->
            <UFormField
              :label="t('admin.settings.site.customEndpoints.title')"
              :description="t('admin.settings.site.customEndpoints.description')"
              :ui="{ container: 'w-full mt-3' }"
            >
              <div class="space-y-3">
                <div
                  v-for="(ep, index) in form.custom_endpoints"
                  :key="index"
                  class="border-t border-default pt-4 first:border-t-0 first:pt-0"
                >
                  <div class="mb-3 flex items-center justify-between">
                    <span class="text-sm font-medium text-toned">
                      {{ t("admin.settings.site.customEndpoints.itemLabel", { n: index + 1, }) }}
                    </span>
                    <UButton
                      icon="i-lucide-trash-2"
                      square
                      @click="removeEndpoint(index)"
                      color="error"
                      variant="ghost"
                    />
                  </div>
                  <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <UFormField :label="t('admin.settings.site.customEndpoints.name')">
                      <UInput
                        v-model="ep.name"
                        type="text"
                        :placeholder="
                          t(
                            'admin.settings.site.customEndpoints.namePlaceholder',
                          )
                        "
                        class="w-full"
                      />
                    </UFormField>
                    <UFormField :label="t('admin.settings.site.customEndpoints.endpointUrl')">
                      <UInput
                        v-model="ep.endpoint"
                        type="url"
                        :placeholder="
                          t(
                            'admin.settings.site.customEndpoints.endpointUrlPlaceholder',
                          )
                        "
                        class="w-full font-mono"
                      />
                    </UFormField>
                    <div class="sm:col-span-2">
                      <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
                        <span class="text-xs font-semibold text-toned">
                          {{ t( "admin.settings.site.customEndpoints.visibilityTitle", ) }}
                        </span>
                        <span class="text-[11px] font-medium text-muted">
                          {{
                            Number(ep.min_recharge) > 0
                              ? t(
                                  "admin.settings.site.customEndpoints.visibilityTier",
                                  { amount: ep.min_recharge },
                                )
                              : t(
                                  "admin.settings.site.customEndpoints.visibilityAll",
                                )
                          }}
                        </span>
                      </div>
                      <div class="flex flex-wrap items-center gap-2">
                        <UButton
                          @click="ep.min_recharge = 0"
                          :color="Number(ep.min_recharge) <= 0 ? 'primary' : 'neutral'"
                          :variant="Number(ep.min_recharge) <= 0 ? 'subtle' : 'outline'"
                          size="sm"
                        >
                          {{ t( "admin.settings.site.customEndpoints.visibilityEveryone", ) }}
                        </UButton>
                        <UButton
                          @click="
                            ep.min_recharge =
                              Number(ep.min_recharge) > 0
                                ? ep.min_recharge
                                : 1000
                          "
                          :color="Number(ep.min_recharge) > 0 ? 'primary' : 'neutral'"
                          :variant="Number(ep.min_recharge) > 0 ? 'subtle' : 'outline'"
                          size="sm"
                        >
                          {{ t( "admin.settings.site.customEndpoints.visibilityThreshold", ) }}
                        </UButton>
                        <template v-if="Number(ep.min_recharge) > 0">
                          <span class="text-xs text-muted">
                            {{ t( "admin.settings.site.customEndpoints.visibilityRechargePrefix", ) }}
                          </span>
                          <UInput
                            :model-value="ep.min_recharge ?? undefined"
                            @update:model-value="ep.min_recharge = $event as number"
                            type="number"
                            min="1"
                            step="1"
                            @blur="normalizeEndpointThreshold(ep)"
                            class="w-28"
                          />
                          <span class="text-xs text-muted">
                            {{ t( "admin.settings.site.customEndpoints.visibilityRechargeSuffix", ) }}
                          </span>
                        </template>
                      </div>
                      <p class="mt-2 text-[11px] text-muted">
                        {{ t( "admin.settings.site.customEndpoints.visibilityHint", ) }}
                      </p>
                    </div>
                    <UFormField
                      :label="t(
                            'admin.settings.site.customEndpoints.descriptionLabel',
                          )"
                      class="sm:col-span-2"
                    >
                      <UInput
                        v-model="ep.description"
                        type="text"
                        :placeholder="
                          t(
                            'admin.settings.site.customEndpoints.descriptionPlaceholder',
                          )
                        "
                        class="w-full"
                      />
                    </UFormField>
                  </div>
                </div>
              </div>
              <UButton icon="i-lucide-plus" @click="addEndpoint" variant="link" class="mt-3 w-full justify-center">
                {{ t("admin.settings.site.customEndpoints.add") }}
              </UButton>
            </UFormField>
            <!-- Contact Info -->
            <UFormField
              :label="t('admin.settings.site.contactInfo')"
              :description="t('admin.settings.site.contactInfoHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
              :ui="{ container: 'w-full sm:max-w-sm' }"
            >
              <UInput
                v-model="form.contact_info"
                type="text"
                :placeholder="t('admin.settings.site.contactInfoPlaceholder')"
                class="w-full"
              />
            </UFormField>
            <!-- Doc URL -->
            <UFormField
              :label="t('admin.settings.site.docUrl')"
              :description="t('admin.settings.site.docUrlHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
              :ui="{ container: 'w-full sm:max-w-sm' }"
            >
              <UInput
                v-model="form.doc_url"
                type="url"
                :placeholder="t('admin.settings.site.docUrlPlaceholder')"
                class="w-full font-mono"
              />
            </UFormField>
            <!-- Site Logo Upload -->
            <UFormField :label="t('admin.settings.site.siteLogo')" :ui="{ container: 'w-full mt-3' }">
              <ImageUpload
                v-model="form.site_logo"
                mode="image"
                :upload-label="t('admin.settings.site.uploadImage')"
                :remove-label="t('admin.settings.site.remove')"
                :hint="t('admin.settings.site.logoHint')"
                :max-size="300 * 1024"
              />
            </UFormField>
            <!-- Home Content -->
            <UFormField
              :label="t('admin.settings.site.homeContent')"
              :description="t('admin.settings.site.homeContentHint')"
              :ui="{ container: 'w-full mt-3' }"
            >
              <UTextarea
                v-model="form.home_content"
                :rows="6"
                :placeholder="t('admin.settings.site.homeContentPlaceholder')"
                class="w-full font-mono"
              />
              <p class="text-xs text-warning mt-2">
                {{ t("admin.settings.site.homeContentIframeWarning") }}
              </p>
            </UFormField>
            <!-- Compact Home Page -->
            <UFormField
              :label="t('admin.settings.site.compactHome')"
              :description="t('admin.settings.site.compactHomeHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.compact_home_enabled" data-testid="compact-home-toggle" />
            </UFormField>
            <!-- Hide CCS Import Button -->
            <UFormField
              :label="t('admin.settings.site.hideCcsImportButton')"
              :description="t('admin.settings.site.hideCcsImportButtonHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.hide_ccs_import_button" />
            </UFormField>
          </SettingsSection>

          <!-- Custom Menu Items -->
          <SettingsSection
            id="settings-custom-menu"
            :title="t('admin.settings.customMenu.title')"
            :description="t('admin.settings.customMenu.description')"
          >
            <!-- Existing menu items -->
            <div
              v-for="(item, index) in form.custom_menu_items"
              :key="item.id || index"
              class="border-t border-default pt-4 first:border-t-0 first:pt-0"
            >
              <div class="mb-3 flex items-center justify-between">
                <span class="text-sm font-medium text-toned">
                  {{ t("admin.settings.customMenu.itemLabel", { n: index + 1 }) }}
                </span>
                <div class="flex items-center gap-2">
                  <!-- Move up -->
                  <UButton
                    icon="i-lucide-chevron-up"
                    square
                    :aria-label="t('admin.settings.customMenu.moveUp')"
                    v-if="index > 0"
                    :title="t('admin.settings.customMenu.moveUp')"
                    @click="moveMenuItem(index, -1)"
                    color="neutral"
                    variant="ghost"
                  />
                  <!-- Move down -->
                  <UButton
                    icon="i-lucide-chevron-down"
                    square
                    :aria-label="t('admin.settings.customMenu.moveDown')"
                    v-if="index < form.custom_menu_items.length - 1"
                    :title="t('admin.settings.customMenu.moveDown')"
                    @click="moveMenuItem(index, 1)"
                    color="neutral"
                    variant="ghost"
                  />
                  <!-- Delete -->
                  <UButton
                    icon="i-lucide-trash-2"
                    square
                    :aria-label="t('admin.settings.customMenu.remove')"
                    :title="t('admin.settings.customMenu.remove')"
                    @click="removeMenuItem(index)"
                    color="error"
                    variant="ghost"
                  />
                </div>
              </div>

              <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <!-- Label -->
                <UFormField :label="t('admin.settings.customMenu.name')">
                  <UInput
                    v-model="item.label"
                    type="text"
                    :placeholder="
                      t('admin.settings.customMenu.namePlaceholder')
                    "
                    class="w-full"
                  />
                </UFormField>

                <!-- Visibility -->
                <UFormField :label="t('admin.settings.customMenu.visibility')">
                  <Select
                    v-model="item.visibility"
                    :options="[{ value: 'user', label: t('admin.settings.customMenu.visibilityUser') }, { value: 'admin', label: t('admin.settings.customMenu.visibilityAdmin') }]"
                    class="w-64"
                  />
                </UFormField>

                <!-- URL (full width) -->
                <UFormField :label="t('admin.settings.customMenu.url')" class="sm:col-span-2">
                  <UInput
                    v-model="item.url"
                    type="url"
                    :placeholder="
                      t('admin.settings.customMenu.urlPlaceholder')
                    "
                    class="w-full font-mono"
                  />
                </UFormField>

                <UFormField
                  :label="t('admin.settings.customMenu.hideOpenButton')"
                  class="flex items-center justify-between gap-4"
                >
                  <Toggle :model-value="item.hide_open_button ?? false" data-testid="custom-menu-hide-open-button" @update:model-value="item.hide_open_button = $event" />
                </UFormField>

                <!-- SVG Icon (full width) -->
                <UFormField :label="t('admin.settings.customMenu.iconSvg')" class="sm:col-span-2">
                  <ImageUpload
                    :model-value="item.icon_svg"
                    mode="svg"
                    size="sm"
                    :upload-label="t('admin.settings.customMenu.uploadSvg')"
                    :remove-label="t('admin.settings.customMenu.removeSvg')"
                    @update:model-value="(v: string) => (item.icon_svg = v)"
                  />
                </UFormField>
              </div>
            </div>
            <!-- Add button -->
            <UButton icon="i-lucide-plus" @click="addMenuItem" variant="link" class="w-full justify-center">
              {{ t("admin.settings.customMenu.add") }}
            </UButton>
          </SettingsSection>
        </div>
        <!-- /Tab: General -->

        <!-- Tab: Login Agreement -->
        <div v-show="activeTab === 'agreement'" id="settings-agreement" class="flex flex-col gap-4 sm:gap-6 lg:gap-12">
	          <SettingsSection>
             <template #actions>
               <UFormField
                 :label="localText('登录条款确认', 'Login agreement')"
                 :description="localText(
	                        '控制登录页是否要求用户先阅读并同意服务条款、隐私政策或其他 Markdown 文档。',
	                        'Control whether the login page requires users to accept Markdown policy documents first.',
	                      )"
                 class="flex max-sm:flex-col justify-between sm:items-center gap-4"
               >
                 <div class="flex items-center gap-3">
	                  <span class="text-sm text-toned">
	                    {{ form.login_agreement_enabled ? localText("已启用", "Enabled") : localText("未启用", "Disabled") }}
	                  </span>
	                  <Toggle v-model="form.login_agreement_enabled" />
	                </div>
               </UFormField>
             </template>
             <div class="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,1fr)_220px]">
               <UFormField
                 :label="localText('展示形式', 'Display mode')"
                 :description="form.login_agreement_mode === 'checkbox'
                      ? localText('复选框会显示在登录按钮下方，未勾选前所有登录入口禁用。', 'The checkbox appears below the login button and gates all login actions.')
                      : localText('弹窗会在登录页打开，用户拒绝后所有登录入口保持禁用。', 'The modal opens on the login page and gates all login actions until accepted.')"
               >
                 <div class="grid grid-cols-2 gap-2 rounded-lg bg-elevated p-1">
                  <UButton
                    icon="i-lucide-shield"
                    @click="form.login_agreement_mode = 'modal'"
                    color="neutral"
                    :variant="form.login_agreement_mode === 'modal' ? 'outline' : 'ghost'"
                    class="justify-center"
                  >
                    {{ localText("弹窗", "Modal") }}
                  </UButton>
                  <UButton
                    icon="i-lucide-circle-check"
                    @click="form.login_agreement_mode = 'checkbox'"
                    color="neutral"
                    :variant="form.login_agreement_mode === 'checkbox' ? 'outline' : 'ghost'"
                    class="justify-center"
                  >
                    {{ localText("复选框", "Checkbox") }}
                  </UButton>
                </div>
               </UFormField>

              <UFormField
                :label="localText('条款更新日期', 'Updated date')"
                :description="localText('日期或文档内容变化后，用户需要重新同意。', 'Changing the date or content requires fresh consent.')"
              >
                <UInput v-model="form.login_agreement_updated_at" type="date" class="w-full" />
              </UFormField>
            </div>
             <UFormField
               :label="localText('协议文档', 'Agreement documents')"
               :description="localText(
                       '文档名称可自定义。正文留空时前台自动使用内置条款（按 OpenLux 结构改写，主体为本站运营方）。可一键填入隐私政策、服务条款、可接受使用政策、免责条款、退款政策、DPA、合规说明和支持地区。',
                       'Titles are customizable. Empty bodies fall back to bundled terms adapted from the OpenLux structure for this site. You can fill Privacy, Terms, AUP, Disclaimer, Refund, DPA, Compliance and Supported regions in one click.',
                     )"
               class="flex max-sm:flex-col justify-between sm:items-center gap-4"
             >
               <div class="flex flex-wrap items-center gap-2">
                 <UButton @click="fillBundledLoginAgreementDocuments" color="neutral" variant="outline" size="sm">
                   {{ localText("填入内置条款", "Fill bundled terms") }}
                 </UButton>
                 <UButton icon="i-lucide-plus" @click="addLoginAgreementDocument" size="sm">
                   {{ localText("添加文档", "Add document") }}
                 </UButton>
               </div>
             </UFormField>
             <div
               v-for="(doc, index) in form.login_agreement_documents"
               :key="doc.id || index"
               class="border-t border-default pt-4 first:border-t-0 first:pt-0"
             >
               <div class="mb-3 flex items-center justify-between gap-3">
                 <div class="flex min-w-0 items-center gap-3">
                   <span
                     class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-elevated text-toned"
                   >
                     <UIcon
                       :name="
                         index === 1
                           ? 'i-lucide-shield'
                           : index === 2
                             ? 'i-lucide-globe'
                             : index === 3
                               ? 'i-lucide-settings'
                               : 'i-lucide-file-text'
                       "
                       class="size-4 shrink-0"
                     />
                   </span>
                   <div class="min-w-0">
                     <p class="truncate text-sm font-semibold text-highlighted">
                       {{ doc.title || localText("未命名文档", "Untitled document") }}
                     </p>
                     <p class="truncate text-xs text-muted">
                       {{ loginAgreementRoutePath(doc, index) }}
                     </p>
                   </div>
                 </div>
                 <UButton
                   icon="i-lucide-trash-2"
                   square
                   :disabled="
                     form.login_agreement_enabled &&
                     form.login_agreement_documents.length <= 1
                   "
                   @click="removeLoginAgreementDocument(index)"
                   color="error"
                   variant="ghost"
                 />
               </div>

               <div class="grid grid-cols-1 gap-3 lg:grid-cols-2">
                 <UFormField :label="localText('文档名称', 'Document title')">
                   <UInput
                     v-model="doc.title"
                     type="text"
                     :placeholder="localText('例如：服务条款', 'Example: Terms of Service')"
                     class="w-full"
                   />
                 </UFormField>
                 <UFormField :label="localText('路由标识', 'Route slug')">
                   <div
                     class="flex overflow-hidden rounded-lg border border-default bg-default focus-within:border-primary-500 focus-within:ring-1 focus-within:ring-primary-500"
                   >
                     <span
                       class="inline-flex flex-shrink-0 items-center border-r border-default bg-elevated px-3 text-sm text-muted"
                     >
                       /legal/
                     </span>
                     <UInput v-model="doc.id" type="text" placeholder="privacy" class="min-w-0 flex-1" />
                   </div>
                 </UFormField>
               </div>
               <UFormField :label="localText('Markdown 内容', 'Markdown content')">
                 <UTextarea
                   v-model="doc.content_md"
                   :rows="8"
                   :placeholder="localText('在这里填写正式 Markdown 内容。', 'Write the final Markdown content here.')"
                   class="w-full font-mono"
                 />
               </UFormField>
             </div>
           </SettingsSection>
        </div>
        <!-- /Tab: Login Agreement -->

        <!-- Tab: Features (功能开关) -->
        <div v-show="activeTab === 'features'" class="flex flex-col gap-4 sm:gap-6 lg:gap-12">
        <SettingsSection id="settings-features-channel-monitor" :title="t('admin.settings.features.channelMonitor.title')">
          <template #description>
            <span class="block">
              {{ t('admin.settings.features.channelMonitor.description') }}
            </span>
            <span class="block mt-1">
              <router-link
                to="/admin/channels/monitor"
                class="inline-flex items-center gap-1 text-primary hover:underline"
              >
                {{ t('admin.settings.features.channelMonitor.configureLink') }}
                <span aria-hidden="true">→</span>
              </router-link>
            </span>
          </template>
          <UFormField
            :label="t('admin.settings.features.channelMonitor.enabled')"
            :description="t('admin.settings.features.channelMonitor.enabledHint')"
            class="flex items-center justify-between gap-4"
          >
            <Toggle v-model="form.channel_monitor_enabled" />
          </UFormField>
          <template v-if="form.channel_monitor_enabled">
            <UFormField :label="t('admin.settings.features.channelMonitor.mode')" :ui="{ container: 'w-full mt-3' }">
              <template #description>
                <span class="block">
                  {{
                    form.channel_monitor_mode === 'v1'
                      ? t('admin.settings.features.channelMonitor.modeV1Hint')
                      : t('admin.settings.features.channelMonitor.modeV2Hint')
                  }}
                </span>
                <span class="block mt-1">
                  {{ t('admin.settings.features.channelMonitor.modeHint') }}
                </span>
              </template>
              <div class="mt-1.5 inline-flex w-full max-w-md rounded-lg bg-elevated p-1">
                <UButton
                  @click="form.channel_monitor_mode = 'v2'"
                  color="neutral"
                  :variant="form.channel_monitor_mode === 'v2' ? 'outline' : 'ghost'"
                  class="flex-1 justify-center"
                >
                  {{ t('admin.settings.features.channelMonitor.modeV2') }}
                </UButton>
                <UButton
                  @click="form.channel_monitor_mode = 'v1'"
                  color="neutral"
                  :variant="form.channel_monitor_mode === 'v1' ? 'outline' : 'ghost'"
                  class="flex-1 justify-center"
                >
                  {{ t('admin.settings.features.channelMonitor.modeV1') }}
                </UButton>
              </div>
            </UFormField>
            <UFormField
              v-if="form.channel_monitor_mode === 'v1'"
              :label="t('admin.settings.features.channelMonitor.defaultInterval')"
              required
              :description="t('admin.settings.features.channelMonitor.defaultIntervalHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
            >
              <UInput
                v-model.number="form.channel_monitor_default_interval_seconds"
                type="number"
                min="15"
                max="3600"
                class="w-32"
              />
            </UFormField>
            <template v-if="form.channel_monitor_mode === 'v2'">
              <UFormField
                :label="t('admin.settings.features.channelMonitor.hideThroughput')"
                :description="t('admin.settings.features.channelMonitor.hideThroughputHint')"
                class="flex items-center justify-between gap-4"
              >
                <Toggle v-model="form.channel_monitor_hide_throughput" />
              </UFormField>
              <UFormField
                :label="t('admin.settings.features.channelMonitor.hideUserRanking')"
                :description="t('admin.settings.features.channelMonitor.hideUserRankingHint')"
                class="flex items-center justify-between gap-4"
              >
                <Toggle v-model="form.channel_monitor_hide_user_ranking" />
              </UFormField>
            </template>
            <UFormField
              v-if="form.channel_monitor_mode === 'v1'"
              :label="t('admin.settings.features.channelMonitor.showQuota')"
              :description="t('admin.settings.features.channelMonitor.showQuotaHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.channel_monitor_show_quota" />
            </UFormField>
          </template>
        </SettingsSection>

        <SettingsSection :title="t('admin.settings.features.availableChannels.title')">
          <template #description>
            <span class="block">
              {{ t('admin.settings.features.availableChannels.description') }}
            </span>
            <span class="block mt-1">
              <router-link
                to="/admin/channels/pricing"
                class="inline-flex items-center gap-1 text-primary hover:underline"
              >
                {{ t('admin.settings.features.availableChannels.configureLink') }}
                <span aria-hidden="true">→</span>
              </router-link>
            </span>
          </template>
          <UFormField
            :label="t('admin.settings.features.availableChannels.enabled')"
            :description="t('admin.settings.features.availableChannels.enabledHint')"
            class="flex items-center justify-between gap-4"
          >
            <Toggle v-model="form.available_channels_enabled" />
          </UFormField>
        </SettingsSection>

        <SettingsSection
          :title="t('admin.settings.features.modelPlaza.title')"
          :description="t('admin.settings.features.modelPlaza.description')"
        >
          <UFormField
            :label="t('admin.settings.features.modelPlaza.enabled')"
            :description="t('admin.settings.features.modelPlaza.enabledHint')"
            class="flex items-center justify-between gap-4"
          >
            <Toggle v-model="form.model_plaza_enabled" />
          </UFormField>
          <UFormField
            v-if="form.model_plaza_enabled"
            :label="t('admin.settings.features.modelPlaza.requireAuth')"
            :description="t('admin.settings.features.modelPlaza.requireAuthHint')"
            class="flex items-center justify-between gap-4"
          >
            <Toggle v-model="form.model_plaza_require_auth" />
          </UFormField>
          <UFormField
            v-if="form.model_plaza_enabled"
            :label="t('admin.settings.features.modelPlaza.priceDescription')"
            :description="t('admin.settings.features.modelPlaza.priceDescriptionHint')"
            :ui="{ container: 'w-full mt-3' }"
          >
            <UTextarea v-model="form.model_plaza_description" :rows="6" class="w-full font-mono" />
          </UFormField>
        </SettingsSection>

        <SettingsSection
          :title="t('admin.settings.features.pluginManagement.title')"
          :description="t('admin.settings.features.pluginManagement.description')"
        >
          <UFormField
            :label="t('admin.settings.features.pluginManagement.enabled')"
            :description="t('admin.settings.features.pluginManagement.enabledHint')"
            class="flex items-center justify-between gap-4"
          >
            <Toggle v-model="form.plugin_management_enabled" />
          </UFormField>
        </SettingsSection>

        <SettingsSection :title="t('admin.settings.features.riskControl.title')">
          <template #description>
            <span class="block">
              {{ t('admin.settings.features.riskControl.description') }}
            </span>
            <span class="block mt-1">
              <router-link to="/admin/risk-control" class="inline-flex items-center gap-1 text-primary hover:underline">
                {{ t('admin.settings.features.riskControl.configureLink') }}
                <span aria-hidden="true">→</span>
              </router-link>
            </span>
          </template>
          <UFormField
            :label="t('admin.settings.features.riskControl.enabled')"
            :description="t('admin.settings.features.riskControl.enabledHint')"
            class="flex items-center justify-between gap-4"
          >
            <Toggle v-model="form.risk_control_enabled" />
          </UFormField>
          <UFormField
            :label="t('admin.settings.features.riskControl.cyberSessionBlock')"
            :description="t('admin.settings.features.riskControl.cyberSessionBlockHint')"
            class="flex items-center justify-between gap-4"
          >
            <Toggle v-model="form.cyber_session_block_enabled" />
          </UFormField>
          <UFormField
            v-if="form.cyber_session_block_enabled"
            :label="t('admin.settings.features.riskControl.cyberSessionBlockTTL')"
            required
            class="flex max-sm:flex-col justify-between items-start gap-4"
          >
            <UInput v-model.number="form.cyber_session_block_ttl_seconds" type="number" min="1" class="w-32" />
          </UFormField>
        </SettingsSection>

        <!-- Affiliate (邀请返利) feature card -->
        <SettingsSection
          :title="t('admin.settings.features.affiliate.title')"
          :description="t('admin.settings.features.affiliate.description')"
        >
          <UFormField
            :label="t('admin.settings.features.affiliate.enabled')"
            :description="t('admin.settings.features.affiliate.enabledHint')"
            class="flex items-center justify-between gap-4"
          >
            <Toggle v-model="form.affiliate_enabled" />
          </UFormField>
          <template v-if="form.affiliate_enabled">
            <UFormField
              :label="t('admin.settings.features.affiliate.adminRechargeRebate')"
              :description="t('admin.settings.features.affiliate.adminRechargeRebateHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.affiliate_admin_recharge_enabled" />
            </UFormField>
            <UFormField
              :label="t('admin.settings.features.affiliate.rebateRate')"
              :description="t('admin.settings.features.affiliate.rebateRateHint')"
              :ui="{ container: 'w-full mt-3' }"
            >
              <div class="relative">
                <UInput
                  v-model.number="form.affiliate_rebate_rate"
                  type="number"
                  step="0.01"
                  min="0"
                  max="100"
                  placeholder="20"
                  class="w-full"
                />
                <span class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-dimmed">%</span>
              </div>
            </UFormField>
            <UFormField
              :label="t('admin.settings.features.affiliate.freezeHours')"
              :description="t('admin.settings.features.affiliate.freezeHoursDesc')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
            >
              <UInput
                v-model.number="form.affiliate_rebate_freeze_hours"
                type="number"
                step="1"
                min="0"
                max="720"
                class="w-32"
              />
            </UFormField>
            <UFormField
              :label="t('admin.settings.features.affiliate.durationDays')"
              :description="t('admin.settings.features.affiliate.durationDaysDesc')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
            >
              <UInput
                v-model.number="form.affiliate_rebate_duration_days"
                type="number"
                step="1"
                min="0"
                max="3650"
                class="w-32"
              />
            </UFormField>
            <UFormField
              :label="t('admin.settings.features.affiliate.perInviteeCap')"
              :description="t('admin.settings.features.affiliate.perInviteeCapDesc')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
            >
              <UInput
                v-model.number="form.affiliate_rebate_per_invitee_cap"
                type="number"
                step="0.01"
                min="0"
                class="w-32"
              />
            </UFormField>
            <!-- 专属用户管理 -->
            <UFormField
              :label="t('admin.settings.features.affiliate.customUsers.title')"
              :description="t('admin.settings.features.affiliate.customUsers.description')"
              class="flex max-sm:flex-col justify-between sm:items-center gap-4"
            >
              <UButton @click="openAffiliateModal(null)" size="sm">
                + {{ t('admin.settings.features.affiliate.customUsers.addButton') }}
              </UButton>
            </UFormField>
            <div class="mb-3 flex items-center gap-2">
              <UInput
                v-model="affiliateState.search"
                type="text"
                :placeholder="t('admin.settings.features.affiliate.customUsers.searchPlaceholder')"
                @input="onAffiliateSearchInput"
                class="flex-1"
              />
              <UButton
                v-if="affiliateState.selected.length > 0"
                @click="openAffiliateBatchModal"
                color="neutral"
                variant="outline"
                size="sm"
              >
                {{ t('admin.settings.features.affiliate.customUsers.batchButton', { count: affiliateState.selected.length }) }}
              </UButton>
            </div>
            <UTable
              :data="affiliateState.entries"
              :columns="affiliateTableColumns"
              :loading="affiliateState.loading"
              :empty="t('admin.settings.features.affiliate.customUsers.empty')"
              :ui="{ th: 'px-3 py-2 text-xs', td: 'px-3 py-2 text-sm' }"
            >
              <template #select-header>
                <UCheckbox
                  :model-value="affiliateState.entries.length > 0 && affiliateState.selected.length === affiliateState.entries.length"
                  @update:model-value="toggleAffiliateSelectAll"
                />
              </template>
              <template #select-cell="{ row }">
                <UCheckbox
                  :model-value="affiliateState.selected.includes(row.original.user_id)"
                  @update:model-value="toggleAffiliateSelect(row.original.user_id)"
                />
              </template>
              <template #email-cell="{ row }">
                <span class="text-highlighted">{{ row.original.email }}</span>
              </template>
              <template #aff_code-cell="{ row }">
                <span class="font-mono">{{ row.original.aff_code }}</span>
                <UBadge v-if="row.original.aff_code_custom" color="primary" variant="subtle" size="sm" class="ml-1">
                  {{ t('admin.settings.features.affiliate.customUsers.customBadge') }}
                </UBadge>
              </template>
              <template #rate-cell="{ row }">
                <span v-if="row.original.aff_rebate_rate_percent != null">{{ row.original.aff_rebate_rate_percent }}%</span>
                <span v-else class="text-dimmed">{{ t('admin.settings.features.affiliate.customUsers.useGlobal') }}</span>
              </template>
              <template #actions-cell="{ row }">
                <div class="flex items-center gap-1">
                  <UButton
                    icon="i-lucide-pencil"
                    color="neutral"
                    variant="ghost"
                    size="sm"
                    square
                    :aria-label="t('common.edit')"
                    @click="openAffiliateModal(row.original)"
                  />
                  <UButton
                    icon="i-lucide-trash-2"
                    color="error"
                    variant="ghost"
                    size="sm"
                    square
                    :aria-label="t('common.delete')"
                    @click="askResetAffiliateUser(row.original)"
                  />
                </div>
              </template>
            </UTable>
            <div
              v-if="affiliateState.total > affiliateState.pageSize"
              class="flex items-center justify-between text-sm"
            >
              <span class="text-muted">
                {{ t('admin.settings.features.affiliate.customUsers.totalLabel', { total: affiliateState.total }) }}
              </span>
              <div class="flex items-center gap-2">
                <UButton
                  :disabled="affiliateState.page <= 1"
                  @click="changeAffiliatePage(affiliateState.page - 1)"
                  color="neutral"
                  variant="outline"
                  size="sm"
                >
                  {{ t('pagination.previous') }}
                </UButton>
                <span class="text-muted">{{ affiliateState.page }} / {{ Math.max(1, Math.ceil(affiliateState.total / affiliateState.pageSize)) }}</span>
                <UButton
                  :disabled="affiliateState.page >= Math.ceil(affiliateState.total / affiliateState.pageSize)"
                  @click="changeAffiliatePage(affiliateState.page + 1)"
                  color="neutral"
                  variant="outline"
                  size="sm"
                >
                  {{ t('pagination.next') }}
                </UButton>
              </div>
            </div>
          </template>
        </SettingsSection>

        <!-- Affiliate add/edit modal -->
        <BaseDialog
          :show="affiliateModal.open"
          :title="affiliateModal.mode === 'add' ? t('admin.settings.features.affiliate.modal.addTitle') : t('admin.settings.features.affiliate.modal.editTitle')"
          width="narrow"
          @close="closeAffiliateModal"
        >
          <div class="space-y-4">
            <UFormField
              v-if="affiliateModal.mode === 'add'"
              :label="t('admin.settings.features.affiliate.modal.userLabel')"
            >
              <UFormField
                v-if="affiliateModal.selectedUser"
                :label="affiliateModal.selectedUser.email"
                class="flex max-sm:flex-col justify-between sm:items-center gap-4"
              >
                <template #description>
                  <span class="ml-1">({{ affiliateModal.selectedUser.username }})</span>
                </template>
                <UButton
                  :title="t('admin.settings.features.affiliate.modal.changeUser')"
                  @click="clearSelectedAffiliateUser"
                  color="error"
                  variant="ghost"
                >
                  ×
                </UButton>
              </UFormField>
              <template v-else>
                <UInput
                  v-model="affiliateModal.userQuery"
                  type="text"
                  :placeholder="t('admin.settings.features.affiliate.modal.userPlaceholder')"
                  @input="onAffiliateUserSearchInput"
                  class="w-full"
                />
                <div
                  v-if="affiliateModal.userResults.length > 0"
                  class="mt-1 max-h-40 overflow-y-auto rounded-sm border border-default"
                >
                  <UButton
                    v-for="u in affiliateModal.userResults"
                    :key="u.id"
                    @click="selectAffiliateUser(u)"
                    color="neutral"
                    variant="ghost"
                    size="sm"
                    class="w-full"
                  >
                    {{ u.email }} <span class="text-xs text-muted">({{ u.username }})</span>
                  </UButton>
                </div>
              </template>
            </UFormField>
            <UFormField v-else :label="t('admin.settings.features.affiliate.modal.userLabel')">
              <UInput
                type="text"
                :value="affiliateModal.editingEntry ? affiliateModal.editingEntry.email : ''"
                disabled
                class="w-full"
              />
            </UFormField>

            <UFormField
              :label="t('admin.settings.features.affiliate.modal.codeLabel')"
              :description="t('admin.settings.features.affiliate.modal.codeHint')"
            >
              <UInput
                v-model="affiliateModal.code"
                type="text"
                :placeholder="t('admin.settings.features.affiliate.modal.codePlaceholder')"
                maxlength="32"
                class="w-full font-mono"
              />
            </UFormField>

            <UFormField
              :label="t('admin.settings.features.affiliate.modal.rateLabel')"
              :description="t('admin.settings.features.affiliate.modal.rateHint')"
            >
              <div class="relative">
                <UInput
                  :model-value="(affiliateModal.rate as string)"
                  @update:model-value="affiliateModal.rate = $event"
                  type="number"
                  step="0.01"
                  min="0"
                  max="100"
                  :placeholder="t('admin.settings.features.affiliate.modal.ratePlaceholder')"
                  class="w-full"
                />
                <span class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-dimmed">%</span>
              </div>
            </UFormField>
          </div>
          <div
            v-if="affiliateModal.mode === 'edit' && affiliateModal.editingEntry"
            class="mt-5 border-t border-default pt-4"
          >
            <div class="flex items-center justify-between gap-2">
              <h4 class="text-sm font-semibold text-highlighted">
                {{ t('admin.settings.features.affiliate.codes.title') }}
              </h4>
              <span>
                {{ t('admin.settings.features.affiliate.codes.quota', { used: affiliateModal.codesQuota.used, max: affiliateModal.codesQuota.max }) }}
              </span>
            </div>
            <p class="mt-1 text-xs text-dimmed">
              {{ t('admin.settings.features.affiliate.codes.hint', { max: affiliateModal.codesQuota.max }) }}
            </p>

            <!-- 默认码只读 -->
            <div class="mt-3 flex items-center gap-2 rounded-md border border-default bg-elevated px-3 py-2">
              <span class="shrink-0">{{ t('admin.settings.features.affiliate.codes.defaultBadge') }}</span>
              <code class="min-w-0 flex-1 truncate font-mono text-sm text-highlighted">{{ affiliateModal.editingEntry.aff_code }}</code>
              <span class="shrink-0 text-xs text-muted">
                {{ t('admin.settings.features.affiliate.codes.stats', { count: affiliateModal.defaultInviteCount, amount: formatCurrency(affiliateModal.defaultRebateAmount) }) }}
              </span>
            </div>

            <p v-if="affiliateModal.codesLoading" class="mt-3 text-xs text-dimmed">
              {{ t('admin.settings.features.affiliate.codes.loading') }}
            </p>
            <p v-else-if="affiliateModal.codes.length === 0" class="mt-3 text-xs text-dimmed">
              {{ t('admin.settings.features.affiliate.codes.empty') }}
            </p>
            <ul v-else class="mt-3 space-y-2">
              <li
                v-for="c in affiliateModal.codes"
                :key="c.code"
                class="flex items-center gap-2 rounded-md border border-default px-3 py-2"
              >
                <span class="shrink-0">
                  {{ c.created_by_admin ? t('admin.settings.features.affiliate.codes.adminBadge') : t('admin.settings.features.affiliate.codes.userBadge') }}
                </span>
                <code class="min-w-0 flex-1 truncate font-mono text-sm text-highlighted">{{ c.code }}</code>
                <span class="shrink-0 text-xs text-muted">
                  {{ t('admin.settings.features.affiliate.codes.stats', { count: c.invite_count ?? 0, amount: formatCurrency(c.rebate_amount ?? 0) }) }}
                </span>
                <span v-if="c.retired" class="shrink-0">{{ t('admin.settings.features.affiliate.codes.retiredBadge') }}</span>
                <UButton
                  v-if="!c.retired"
                  :disabled="affiliateModal.codesSaving"
                  @click="removeAffiliateCode(c.code)"
                  color="error"
                  variant="soft"
                  size="sm"
                  class="shrink-0"
                >
                  {{ affiliateModal.codesSaving ? t('admin.settings.features.affiliate.codes.removing') : t('admin.settings.features.affiliate.codes.remove') }}
                </UButton>
              </li>
            </ul>

            <div class="mt-3 flex items-center gap-2">
              <UInput
                v-model="affiliateModal.newCode"
                type="text"
                maxlength="32"
                :placeholder="t('admin.settings.features.affiliate.codes.placeholder')"
                @keyup.enter="addAffiliateCode"
                class="flex-1 font-mono"
              />
              <UButton
                :disabled="affiliateModal.codesSaving || !affiliateModal.newCode.trim() || affiliateModal.codesQuota.limit <= 0"
                @click="addAffiliateCode"
                color="neutral"
                variant="outline"
                class="shrink-0"
              >
                {{ affiliateModal.codesSaving ? t('admin.settings.features.affiliate.codes.adding') : t('admin.settings.features.affiliate.codes.addButton') }}
              </UButton>
            </div>
          </div>
          <div class="mt-6 flex items-center justify-between gap-3">
            <p v-if="!affiliateModalCanSubmit" class="text-xs text-muted">
              {{ t('admin.settings.features.affiliate.modal.errorEmpty') }}
            </p>
            <span v-else></span>
            <div class="flex gap-2">
              <UButton @click="closeAffiliateModal" color="neutral" variant="outline">
                {{ t('common.cancel') }}
              </UButton>
              <UButton :disabled="affiliateModal.saving || !affiliateModalCanSubmit" @click="submitAffiliateModal">
                {{ affiliateModal.saving ? t('common.saving') : t('common.save') }}
              </UButton>
            </div>
          </div>
        </BaseDialog>

        <!-- Affiliate batch rate modal -->
        <BaseDialog
          :show="affiliateBatchModal.open"
          :title="t('admin.settings.features.affiliate.batchModal.title', { count: affiliateState.selected.length })"
          width="narrow"
          @close="affiliateBatchModal.open = false"
        >
          <p class="mb-4 text-sm text-muted">
            {{ t('admin.settings.features.affiliate.batchModal.hint') }}
          </p>
          <div class="relative">
            <UInput
              :model-value="(affiliateBatchModal.rate as string)"
              @update:model-value="affiliateBatchModal.rate = $event"
              type="number"
              step="0.01"
              min="0"
              max="100"
              :placeholder="t('admin.settings.features.affiliate.batchModal.placeholder')"
              class="w-full"
            />
            <span class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-dimmed">%</span>
          </div>
          <p class="mt-2 text-xs text-dimmed">
            {{ t('admin.settings.features.affiliate.batchModal.clearHint') }}
          </p>
          <template #footer>
            <div class="flex justify-end gap-3">
              <UButton @click="affiliateBatchModal.open = false" color="neutral" variant="outline">
                {{ t('common.cancel') }}
              </UButton>
              <UButton :disabled="affiliateBatchModal.saving" @click="submitAffiliateBatchModal">
                {{ affiliateBatchModal.saving ? t('common.saving') : t('common.save') }}
              </UButton>
            </div>
          </template>
        </BaseDialog>
        </div><!-- /Tab: Features -->

        <!-- Tab: Email -->
        <!-- Tab: Payment -->
        <div v-show="activeTab === 'payment'" class="flex flex-col gap-4 sm:gap-6 lg:gap-12">
          <!-- Payment System Settings -->
          <SettingsSection id="settings-payment" :title="t('admin.settings.payment.title')">
            <template #description>
              <span>
                {{ t("admin.settings.payment.description") }}
                <a
                  :href="paymentGuideHref"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="ml-2 inline-flex items-center text-primary hover:underline"
                >
                  <UIcon name="i-lucide-external-link" class="size-3.5 mr-0.5" />
                  {{ t("admin.settings.payment.configGuide") }}
                </a>
              </span>
            </template>
            <!-- Enable toggle -->
            <UFormField
              :label="t('admin.settings.payment.enabled')"
              :description="t('admin.settings.payment.enabledHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.payment_enabled" />
            </UFormField>
            <template v-if="form.payment_enabled">
              <UFormField
                :label="t('admin.settings.payment.rechargeCenterEnabled')"
                :description="t('admin.settings.payment.rechargeCenterEnabledHint')"
                class="flex items-center justify-between gap-4"
              >
                <Toggle
                  v-model="form.payment_recharge_center_enabled"
                  :aria-label="t('admin.settings.payment.rechargeCenterEnabled')"
                />
              </UFormField>
              <!-- Product name -->
              <UFormField
                :label="t('admin.settings.payment.productNamePrefix') + ' / ' + t('admin.settings.payment.productNameSuffix')"
                :help="t('admin.settings.payment.preview') + ': ' + (form.payment_product_name_prefix || 'Sub2API') + ' 100 ' + (form.payment_product_name_suffix || 'CNY')"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <div class="flex gap-2">
                  <UInput v-model="form.payment_product_name_prefix" type="text" placeholder="Sub2API" :aria-label="t('admin.settings.payment.productNamePrefix')" class="w-36" />
                  <UInput v-model="form.payment_product_name_suffix" type="text" placeholder="CNY" :aria-label="t('admin.settings.payment.productNameSuffix')" class="w-28" />
                </div>
              </UFormField>
              <!-- Amounts -->
              <UFormField :label="t('admin.settings.payment.minAmount')" class="flex max-sm:flex-col justify-between items-start gap-4">
                <UInput
                  :value="form.payment_min_amount || ''"
                  @input="form.payment_min_amount = parseFloat(($event.target as HTMLInputElement).value) || 0"
                  type="number"
                  step="0.01"
                  min="0"
                  :placeholder="t('admin.settings.payment.noLimit')"
                  class="w-40"
                />
              </UFormField>
              <UFormField :label="t('admin.settings.payment.maxAmount')" class="flex max-sm:flex-col justify-between items-start gap-4">
                <UInput
                  :value="form.payment_max_amount || ''"
                  @input="form.payment_max_amount = parseFloat(($event.target as HTMLInputElement).value) || 0"
                  type="number"
                  step="0.01"
                  min="0"
                  :placeholder="t('admin.settings.payment.noLimit')"
                  class="w-40"
                />
              </UFormField>
              <UFormField :label="t('admin.settings.payment.dailyLimit')" class="flex max-sm:flex-col justify-between items-start gap-4">
                <UInput
                  :value="form.payment_daily_limit || ''"
                  @input="form.payment_daily_limit = parseFloat(($event.target as HTMLInputElement).value) || 0"
                  type="number"
                  step="0.01"
                  min="0"
                  :placeholder="t('admin.settings.payment.noLimit')"
                  class="w-40"
                />
              </UFormField>
              <UFormField
                :label="t('admin.settings.payment.balanceRechargeMultiplier')"
                :description="t('admin.settings.payment.balanceRechargeMultiplierHint')"
                :help="t('admin.settings.payment.balanceRechargePreview', { usd: (Number(form.payment_balance_recharge_multiplier) || 1).toFixed(2) })"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <UInput
                  :value="form.payment_balance_recharge_multiplier || ''"
                  @input="form.payment_balance_recharge_multiplier = parseFloat(($event.target as HTMLInputElement).value) || 1"
                  type="number"
                  step="0.01"
                  min="0.01"
                  class="w-40"
                />
              </UFormField>
              <UFormField
                :label="t('admin.settings.payment.subscriptionUsdToCnyRate')"
                :description="t('admin.settings.payment.subscriptionUsdToCnyRateHint')"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <UInput
                  :value="form.payment_subscription_usd_to_cny_rate || ''"
                  @input="form.payment_subscription_usd_to_cny_rate = parseFloat(($event.target as HTMLInputElement).value) || 0"
                  type="number"
                  step="0.01"
                  min="0"
                  :placeholder="t('admin.settings.payment.subscriptionUsdToCnyRateDisabled')"
                  class="w-40"
                />
              </UFormField>
              <UFormField
                :label="t('admin.settings.payment.rechargeFeeRate')"
                :description="t('admin.settings.payment.rechargeFeeRateHint')"
                :help="(Number(form.payment_recharge_fee_rate) || 0) > 0 ? t('admin.settings.payment.rechargeFeePreview', { fee: (Number(form.payment_recharge_fee_rate) || 0).toFixed(2) }) : undefined"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <UInput
                  :value="form.payment_recharge_fee_rate ?? ''"
                  @input="form.payment_recharge_fee_rate = Math.min(100, Math.max(0, Math.round(parseFloat(($event.target as HTMLInputElement).value || '0') * 100) / 100))"
                  type="number"
                  step="0.01"
                  min="0"
                  max="100"
                  class="w-40"
                >
                  <template #trailing><span class="text-xs text-dimmed">%</span></template>
                </UInput>
              </UFormField>
              <UFormField
                :label="t('admin.settings.payment.orderTimeout')"
                required
                :description="t('admin.settings.payment.orderTimeoutHint')"
                class="flex max-sm:flex-col justify-between items-start gap-4"
              >
                <UInput v-model.number="form.payment_order_timeout_minutes" type="number" min="1" required class="w-40" />
              </UFormField>
              <UFormField :label="t('admin.settings.payment.maxPendingOrders')" class="flex max-sm:flex-col justify-between items-start gap-4">
                <UInput v-model.number="form.payment_max_pending_orders" type="number" min="1" class="w-40" />
              </UFormField>
              <UFormField :label="t('admin.settings.payment.loadBalanceStrategy')" class="flex max-sm:flex-col justify-between items-start gap-4">
                <Select v-model="form.payment_load_balance_strategy" :options="loadBalanceOptions" class="w-40" />
              </UFormField>
              <!-- Cancel rate limit -->
              <UFormField :label="t('admin.settings.payment.cancelRateLimit')" :ui="{ container: 'w-full mt-3' }">
                <div class="flex flex-wrap items-center gap-2 text-sm">
                  <Toggle v-model="form.payment_cancel_rate_limit_enabled" :aria-label="t('admin.settings.payment.cancelRateLimit')" />
                  <Select
                    v-model="form.payment_cancel_rate_limit_window_mode"
                    :options="cancelRateLimitModeOptions"
                    class="w-24"
                    :disabled="!form.payment_cancel_rate_limit_enabled"
                  />
                  <span :class="form.payment_cancel_rate_limit_enabled ? 'text-toned' : 'text-dimmed'">{{ t("admin.settings.payment.cancelRateLimitEvery") }}</span>
                  <UInput
                    v-model.number="form.payment_cancel_rate_limit_window"
                    type="number"
                    min="1"
                    required
                    :disabled="!form.payment_cancel_rate_limit_enabled"
                    class="w-16"
                  />
                  <Select
                    v-model="form.payment_cancel_rate_limit_unit"
                    :options="cancelRateLimitUnitOptions"
                    class="w-28"
                    :disabled="!form.payment_cancel_rate_limit_enabled"
                  />
                  <span :class="form.payment_cancel_rate_limit_enabled ? 'text-toned' : 'text-dimmed'">{{ t("admin.settings.payment.cancelRateLimitAllowMax") }}</span>
                  <UInput
                    v-model.number="form.payment_cancel_rate_limit_max"
                    type="number"
                    min="1"
                    required
                    :disabled="!form.payment_cancel_rate_limit_enabled"
                    class="w-16"
                  />
                  <span :class="form.payment_cancel_rate_limit_enabled ? 'text-toned' : 'text-dimmed'">{{ t("admin.settings.payment.cancelRateLimitTimes") }}</span>
                </div>
              </UFormField>
              <UFormField
                :label="t('admin.settings.payment.alipayForceQRCode')"
                :description="t('admin.settings.payment.alipayForceQRCodeHint')"
                class="flex items-center justify-between gap-4"
              >
                <Toggle
                  :model-value="form.payment_alipay_force_qrcode ?? false"
                  @update:model-value="form.payment_alipay_force_qrcode = $event"
                />
              </UFormField>
              <UFormField
                :label="t('admin.settings.payment.alipayMobilePrecreateDeepLink')"
                :description="t('admin.settings.payment.alipayMobilePrecreateDeepLinkHint')"
                class="flex items-center justify-between gap-4"
              >
                <Toggle
                  :model-value="form.payment_alipay_mobile_precreate_deep_link ?? false"
                  @update:model-value="form.payment_alipay_mobile_precreate_deep_link = $event"
                />
              </UFormField>
              <!-- Row 4: Enabled payment types (provider badges like sub2apipay) -->
              <UFormField :label="t('admin.settings.payment.enabledPaymentTypes')" :ui="{ container: 'w-full mt-3' }">
                <template #description>
                  <span>
                    {{ t("admin.settings.payment.enabledPaymentTypesHint") }}
                    <a
                      :href="paymentMethodsHref"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="ml-1 text-primary hover:underline"
                    >
                      {{ t("admin.settings.payment.findProvider") }}
                      <UIcon name="i-lucide-external-link" class="size-3.5 mb-0.5 ml-0.5 inline" />
                    </a>
                  </span>
                </template>
                <div class="mt-1.5 flex flex-wrap gap-2">
                  <UButton
                    v-for="pt in allPaymentTypes"
                    :key="pt.value"
                    @click="togglePaymentType(pt.value)"
                    :color="isPaymentTypeEnabled(pt.value) ? 'primary' : 'neutral'"
                    :variant="isPaymentTypeEnabled(pt.value) ? 'subtle' : 'outline'"
                  >
                    {{ pt.label }}
                  </UButton>
                </div>
              </UFormField>
              <!-- Row 5: Help image + text -->
              <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <UFormField :label="t('admin.settings.payment.helpImage')">
                  <ImageUpload
                    v-model="form.payment_help_image_url"
                    :upload-label="t('admin.settings.site.uploadImage')"
                    :remove-label="t('admin.settings.site.remove')"
                    :placeholder="
                      t('admin.settings.payment.helpImagePlaceholder')
                    "
                  />
                </UFormField>
                <UFormField :label="t('admin.settings.payment.helpText')">
                  <UTextarea
                    v-model="form.payment_help_text"
                    :rows="3"
                    :placeholder="
                      t('admin.settings.payment.helpTextPlaceholder')
                    "
                    class="w-full"
                  />
                </UFormField>
              </div>
            </template>
          </SettingsSection>

          <!-- Provider Management -->
          <PaymentProviderList
            v-if="form.payment_enabled"
            :providers="providers"
            :loading="providersLoading"
            :can-create="hasAnyPaymentTypeEnabled"
            :enabled-payment-types="form.payment_enabled_types"
            :all-payment-types="allPaymentTypes"
            :redirect-label="t('admin.settings.payment.easypayRedirect')"
            @refresh="loadProviders"
            @create="openCreateProvider"
            @edit="openEditProvider"
            @delete="confirmDeleteProvider"
            @toggle-field="handleToggleField"
            @toggle-type="handleToggleType"
            @reorder="handleReorderProviders"
          />
        </div>

        <div v-show="activeTab === 'email'" class="flex flex-col gap-4 sm:gap-6 lg:gap-12">
          <!-- Email disabled hint - show when email_verify_enabled is off -->
          <SettingsSection
            v-if="!form.email_verify_enabled"
            :title="t('admin.settings.emailTabDisabledTitle')"
            :description="t('admin.settings.emailTabDisabledHint')"
          >

          </SettingsSection>

          <!-- SMTP Settings - Only show when email verification is enabled -->
          <SettingsSection
            v-if="form.email_verify_enabled"
            id="settings-smtp"
            :title="t('admin.settings.smtp.title')"
            :description="t('admin.settings.smtp.description')"
          >
            <template #actions>
              <UButton
                :loading="testingSmtp"
                @click="testSmtpConnection"
                :disabled="testingSmtp || loadFailed"
                color="neutral"
                variant="outline"
              >
                {{ testingSmtp ? t("admin.settings.smtp.testing") : t("admin.settings.smtp.testConnection") }}
              </UButton>
            </template>
            <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
              <UFormField :label="t('admin.settings.smtp.host')">
                <UInput
                  v-model="form.smtp_host"
                  type="text"
                  :placeholder="t('admin.settings.smtp.hostPlaceholder')"
                  class="w-full"
                />
              </UFormField>
              <UFormField :label="t('admin.settings.smtp.port')">
                <UInput
                  v-model.number="form.smtp_port"
                  type="number"
                  min="1"
                  max="65535"
                  :placeholder="t('admin.settings.smtp.portPlaceholder')"
                  class="w-full"
                />
              </UFormField>
              <UFormField :label="t('admin.settings.smtp.username')">
                <UInput
                  v-model="form.smtp_username"
                  type="text"
                  :placeholder="t('admin.settings.smtp.usernamePlaceholder')"
                  class="w-full"
                />
              </UFormField>
              <UFormField
                :label="t('admin.settings.smtp.password')"
                :description="form.smtp_password_configured
                      ? t('admin.settings.smtp.passwordConfiguredHint')
                      : t('admin.settings.smtp.passwordHint')"
              >
                <UInput
                  v-model="form.smtp_password"
                  type="password"
                  autocomplete="new-password"
                  autocapitalize="off"
                  spellcheck="false"
                  @keydown="smtpPasswordManuallyEdited = true"
                  @paste="smtpPasswordManuallyEdited = true"
                  :placeholder="
                    form.smtp_password_configured
                      ? t('admin.settings.smtp.passwordConfiguredPlaceholder')
                      : t('admin.settings.smtp.passwordPlaceholder')
                  "
                  class="w-full"
                />
              </UFormField>
              <UFormField :label="t('admin.settings.smtp.fromEmail')">
                <UInput
                  v-model="form.smtp_from_email"
                  type="email"
                  :placeholder="t('admin.settings.smtp.fromEmailPlaceholder')"
                  class="w-full"
                />
              </UFormField>
              <UFormField :label="t('admin.settings.smtp.fromName')">
                <UInput
                  v-model="form.smtp_from_name"
                  type="text"
                  :placeholder="t('admin.settings.smtp.fromNamePlaceholder')"
                  class="w-full"
                />
              </UFormField>
              <UFormField
                :label="t('admin.settings.smtp.ticketAdminEmail')"
                :description="t('admin.settings.smtp.ticketAdminEmailHint')"
                class="sm:col-span-2"
              >
                <UInput
                  v-model="form.ticket_admin_email"
                  type="email"
                  :placeholder="
                    t('admin.settings.smtp.ticketAdminEmailPlaceholder')
                  "
                  class="w-full"
                />
              </UFormField>
            </div>
            <!-- Use TLS Toggle -->
            <UFormField
              :label="t('admin.settings.smtp.useTls')"
              :description="t('admin.settings.smtp.useTlsHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.smtp_use_tls" />
            </UFormField>
          </SettingsSection>

          <!-- Send Test Email - Only show when email verification is enabled -->
          <SettingsSection
            v-if="form.email_verify_enabled"
            :title="t('admin.settings.testEmail.title')"
            :description="t('admin.settings.testEmail.description')"
          >
            <div class="flex items-end gap-4">
              <UFormField :label="t('admin.settings.testEmail.recipientEmail')">
                <UInput
                  v-model="testEmailAddress"
                  type="email"
                  :placeholder="
                    t('admin.settings.testEmail.recipientEmailPlaceholder')
                  "
                  class="w-full"
                />
              </UFormField>
              <UButton
                :loading="sendingTestEmail"
                @click="sendTestEmail"
                :disabled="
                  sendingTestEmail || !testEmailAddress || loadFailed
                "
                color="neutral"
                variant="outline"
              >
                {{
                  sendingTestEmail
                    ? t("admin.settings.testEmail.sending")
                    : t("admin.settings.testEmail.sendTestEmail")
                }}
              </UButton>
            </div>
          </SettingsSection>

          <!-- 订阅到期提醒 -->
          <SettingsSection
            :title="t('admin.settings.subscriptionExpiryNotify.title')"
            :description="t('admin.settings.subscriptionExpiryNotify.description')"
          >
            <UFormField
              :label="t('admin.settings.subscriptionExpiryNotify.enabled')"
              :description="t('admin.settings.subscriptionExpiryNotify.enabledHint')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.subscription_expiry_notify_enabled" />
            </UFormField>
          </SettingsSection>

          <EmailTemplateEditor />

          <!-- Balance Low Notification -->
          <SettingsSection
            :title="t('admin.settings.balanceNotify.title')"
            :description="t('admin.settings.balanceNotify.description')"
          >
            <UFormField
              :label="t('admin.settings.balanceNotify.enabled')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.balance_low_notify_enabled" />
            </UFormField>
            <UFormField
              v-if="form.balance_low_notify_enabled"
              :label="t('admin.settings.balanceNotify.threshold')"
              :description="t('admin.settings.balanceNotify.thresholdHint')"
              :ui="{ container: 'w-full mt-3' }"
            >
              <div class="relative">
                <span class="absolute left-3 top-1/2 -translate-y-1/2 text-dimmed">$</span>
                <UInput
                  v-model.number="form.balance_low_notify_threshold"
                  type="number"
                  min="0"
                  step="0.01"
                  class="w-full"
                />
              </div>
            </UFormField>
            <UFormField
              :label="t('admin.settings.balanceNotify.rechargeUrl')"
              :description="t('admin.settings.balanceNotify.rechargeUrlHint')"
              class="flex max-sm:flex-col justify-between items-start gap-4"
              :ui="{ container: 'w-full sm:max-w-sm' }"
            >
              <UInput
                v-model="form.balance_low_notify_recharge_url"
                type="url"
                :placeholder="currentOrigin"
                class="w-full"
              />
            </UFormField>
          </SettingsSection>

          <!-- Account Quota Notification -->
          <SettingsSection
            :title="t('admin.settings.quotaNotify.title')"
            :description="t('admin.settings.quotaNotify.description')"
          >
            <UFormField
              :label="t('admin.settings.quotaNotify.enabled')"
              class="flex items-center justify-between gap-4"
            >
              <Toggle v-model="form.account_quota_notify_enabled" />
            </UFormField>
            <UFormField
              v-if="form.account_quota_notify_enabled"
              :label="t('admin.settings.quotaNotify.emails')"
              :description="t('admin.settings.quotaNotify.emailsHint')"
              :ui="{ container: 'w-full mt-3' }"
            >
              <div class="space-y-2">
                <div
                  v-for="(entry, index) in form.account_quota_notify_emails ||
                  []"
                  :key="index"
                  class="flex items-center gap-2"
                >
                  <Toggle :model-value="!entry.disabled" class="shrink-0" @update:model-value="entry.disabled = !entry.disabled" />
                  <UInput
                    v-model="entry.email"
                    type="email"
                    :placeholder="t('admin.settings.quotaNotify.emailPlaceholder')"
                    class="flex-1"
                  />
                  <UButton
                    icon="i-lucide-x"
                    square
                    color="neutral"
                    variant="ghost"
                    :aria-label="t('common.delete')"
                    @click="form.account_quota_notify_emails.splice(index, 1)"
                  />
                </div>
                <UButton icon="i-lucide-plus" color="neutral" variant="outline" size="sm" @click="addQuotaNotifyEmail">
                  {{ t("admin.settings.quotaNotify.addEmail") }}
                </UButton>
              </div>
            </UFormField>
          </SettingsSection>
        </div>
        <!-- /Tab: Email -->

        <!-- Tab: Backup -->
        <div v-show="activeTab === 'backup'" id="settings-backup">
          <BackupSettings />
        </div>

        <!-- Tab: Appearance (site theme; the panel loads and saves on its own) -->
        <div v-if="activeTab === 'appearance'" data-testid="settings-appearance">
          <ThemeCustomizer />
        </div>
        </div>
      </UForm>

      <!-- Provider dialogs placed outside the settings form to prevent form submission bubbling -->
      <PaymentProviderDialog
        ref="providerDialogRef"
        :show="showProviderDialog"
        :saving="providerSaving"
        :editing="editingProvider"
        :all-key-options="providerKeyOptions"
        :enabled-key-options="enabledProviderKeyOptions"
        :all-payment-types="allPaymentTypes"
        :redirect-label="t('admin.settings.payment.easypayRedirect')"
        @close="showProviderDialog = false"
        @save="handleSaveProvider"
      />
      <ConfirmDialog
        :show="showDeleteProviderDialog"
        :title="t('admin.settings.payment.deleteProvider')"
        :message="t('admin.settings.payment.deleteProviderConfirm')"
        :confirm-text="t('common.delete')"
        danger
        @confirm="handleDeleteProvider"
        @cancel="showDeleteProviderDialog = false"
      />
      <ConfirmDialog
        :show="affiliateConfirmDialog.show"
        :title="affiliateConfirmDialog.title"
        :message="affiliateConfirmDialog.message"
        :confirm-text="affiliateConfirmDialog.confirmText"
        danger
        @confirm="handleAffiliateConfirm"
        @cancel="cancelAffiliateConfirm"
      />
      <!-- 关闭 step-up 开关等敏感保存操作触发的 TOTP 二次验证 -->
      <TotpStepUpDialog :controller="settingsStepUp" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, getCurrentInstance, nextTick, onMounted, watch } from "vue";
import { useI18n } from "vue-i18n";
import type { TableColumn, TabsItem } from "@nuxt/ui";
import { adminAPI } from "@/api";
import {
  appendAuthSourceDefaultsToUpdateRequest,
  buildAuthSourceDefaultsState,
  normalizeAccountSchedulingThresholdsMap,
  normalizePlatformQuotasMap,
  sanitizeAccountSchedulingThresholdsMap,
  sanitizePlatformQuotasMap,
  SCHEDULING_THRESHOLD_PLATFORMS,
  defaultWeChatConnectScopesForMode,
  deriveWeChatConnectStoredMode,
  normalizeDefaultSubscriptionSettings,
  resolveWeChatConnectModeCapabilities,
} from "@/api/admin/settings";
import type {
  AuthSourceDefaultsState,
  AuthSourceType,
  SystemSettings,
  UpdateSettingsRequest,
  DefaultSubscriptionSetting,
  DefaultPlatformQuotasMap,
  OpenAIFastPolicyRule,
  WeChatConnectMode,
  WebSearchEmulationConfig,
  WebSearchProviderConfig,
  WebSearchTestResult,
} from "@/api/admin/settings";
import type {
  AdminGroup,
  LoginAgreementDocument,
  NotifyEmailEntry,
  Proxy,
} from "@/types";
import type { ProviderInstance } from "@/types/payment";
import AppLayout from "@/components/layout/AppLayout.vue";
import Select from "@/components/common/Select.vue";
import ConfirmDialog from "@/components/common/ConfirmDialog.vue";
import BaseDialog from "@/components/common/BaseDialog.vue";
import PaymentProviderList from "@/components/payment/PaymentProviderList.vue";
import PaymentProviderDialog from "@/components/payment/PaymentProviderDialog.vue";
import GroupBadge from "@/components/common/GroupBadge.vue";
import GroupOptionItem from "@/components/common/GroupOptionItem.vue";
import Toggle from "@/components/common/Toggle.vue";
import ProxySelector from "@/components/common/ProxySelector.vue";
import ImageUpload from "@/components/common/ImageUpload.vue";
import BackupSettings from "@/views/admin/BackupView.vue";
import ThemeCustomizer from "@/components/admin/theme/ThemeCustomizer.vue";
import AdminPageHeader from "@/components/admin/page/AdminPageHeader.vue";
import SettingsSection from "@/views/admin/settings/SettingsSection.vue";
import GitHubMark from "@/components/auth/GitHubMark.vue";
import GoogleMark from "@/components/auth/GoogleMark.vue";
import EmailTemplateEditor from "@/views/admin/settings/EmailTemplateEditor.vue";
import GatewayIPNoticeSettings from "@/views/admin/settings/GatewayIPNoticeSettings.vue";
import OpenAIFastPolicyUserSelector from "@/views/admin/settings/OpenAIFastPolicyUserSelector.vue";
import { useClipboard } from "@/composables/useClipboard";
import {
  useStepUp,
  isStepUpCancelled,
  isStepUpBlocked,
  stepUpBlockReason,
} from "@/composables/useStepUp";
import TotpStepUpDialog from "@/components/auth/TotpStepUpDialog.vue";
import { affiliatesAPI, type AffiliateAdminEntry, type SimpleUser as AffiliateSimpleUser } from "@/api/admin/affiliates";
import type {
  AdminUserAffiliateCode,
  AdminUserAffiliateCodeQuota,
} from "@/api/admin/affiliates";
import { extractApiErrorMessage, extractI18nErrorMessage } from "@/utils/apiError";
import { formatCurrency } from "@/utils/format";
import { parseTableDefaultPageSizeInput, parseTablePageSizeOptionsInput } from "@/utils/tablePreferences";
import { useAppStore } from "@/stores";
import { useAdminSettingsStore } from "@/stores/adminSettings";
import { normalizeVisibleMethod } from "@/components/payment/paymentFlow";
import {
  bundledLegalDocuments,
  resolveLegalDocumentId,
} from "@/legal/catalog";
import {
  isRegistrationEmailSuffixDomainValid,
  normalizeRegistrationEmailSuffixDomain,
  normalizeRegistrationEmailSuffixDomains,
  parseRegistrationEmailSuffixWhitelistInput,
} from "@/utils/registrationEmailPolicy";
import {
  parseFingerprintSignalsToRows,
  serializeFingerprintRowsToJSON,
  defaultFingerprintSignalRows,
  type FingerprintSignalRow,
} from "./codexFingerprintSignals";

const { t, locale } = useI18n();
const appStore = useAppStore();
// 关闭 step-up 开关是敏感操作：后端返回 STEP_UP_REQUIRED 时弹 TOTP 码重试
const settingsStepUp = useStepUp();
const adminSettingsStore = useAdminSettingsStore();
const isZhLocale = computed(() => locale.value.startsWith("zh"));

function localText(zh: string, en: string): string {
  return isZhLocale.value ? zh : en;
}

const paymentGuideHref = computed(() =>
  locale.value.startsWith("zh")
    ? "https://github.com/Wei-Shaw/sub2api/blob/main/docs/PAYMENT_CN.md"
    : "https://github.com/Wei-Shaw/sub2api/blob/main/docs/PAYMENT.md",
);

const paymentMethodsHref = computed(() =>
  locale.value.startsWith("zh")
    ? "https://github.com/Wei-Shaw/sub2api/blob/main/docs/PAYMENT_CN.md#支持的支付方式"
    : "https://github.com/Wei-Shaw/sub2api/blob/main/docs/PAYMENT.md#supported-payment-methods",
);

type SettingsTab =
  | "general"
  | "appearance"
  | "agreement"
  | "features"
  | "security"
  | "users"
  | "gateway"
  | "payment"
  | "email"
  | "backup";
const activeTab = ref<SettingsTab>("general");
const settingsTabs = [
  { key: "general" as SettingsTab, icon: "i-lucide-house" },
  { key: "appearance" as SettingsTab, icon: "i-lucide-palette" },
  { key: "agreement" as SettingsTab, icon: "i-lucide-file-text" },
  { key: "features" as SettingsTab, icon: "i-lucide-zap" },
  { key: "security" as SettingsTab, icon: "i-lucide-shield" },
  { key: "users" as SettingsTab, icon: "i-lucide-user" },
  { key: "gateway" as SettingsTab, icon: "i-lucide-server" },
  { key: "payment" as SettingsTab, icon: "i-lucide-credit-card" },
  { key: "email" as SettingsTab, icon: "i-lucide-mail" },
  { key: "backup" as SettingsTab, icon: "i-lucide-database" },
];

// UTabs (reka Tabs) owns arrow / Home / End keys and activates on mousedown and focus.
const settingsTabItems = computed<TabsItem[]>(() =>
  settingsTabs.map((tab) => ({
    label: t(`admin.settings.tabs.${tab.key}`),
    icon: tab.icon,
    value: tab.key,
  })),
);

function selectSettingsTab(tab: SettingsTab): void {
  activeTab.value = tab;
}

// ── Command-palette deep links: /admin/settings?tab=security&section=settings-linuxdo ──
// The palette passes the tab and the section id; the section is scrolled into view
// and briefly outlined. Without a router (unit tests, embedded preview) this is a no-op.
const settingsInstance = getCurrentInstance();
const knownSettingsTabs = new Set<SettingsTab>(settingsTabs.map((tab) => tab.key));

function currentSettingsRoute():
  | { query?: Record<string, unknown>; fullPath?: string }
  | undefined {
  return settingsInstance?.appContext.config.globalProperties.$route as
    | { query?: Record<string, unknown>; fullPath?: string }
    | undefined;
}

function applySettingsDeepLink(): void {
  const route = currentSettingsRoute();
  if (!route) return;
  const query = route.query ?? {};
  const tab = typeof query.tab === "string" ? query.tab : "";
  const section = typeof query.section === "string" ? query.section : "";
  if (knownSettingsTabs.has(tab as SettingsTab)) {
    activeTab.value = tab as SettingsTab;
  }
  if (!section) return;
  void nextTick(() => {
    const target = document.getElementById(section);
    if (!target) return;
    target.classList.add("settings-section-highlight");
    try {
      target.scrollIntoView?.({ block: "start", behavior: "smooth" });
    } catch {
      // jsdom and other layouts without scrolling still get the highlight.
    }
    window.setTimeout(() => target.classList.remove("settings-section-highlight"), 1600);
  });
}

onMounted(applySettingsDeepLink);
watch(
  () => currentSettingsRoute()?.fullPath,
  () => applySettingsDeepLink(),
);
// Platform quota matrices (users tab: system defaults and per auth source).
const platformQuotaRows = (["anthropic", "openai", "gemini", "antigravity", "grok"] as const).map((platform) => ({ platform }));
const platformQuotaColumns = computed<TableColumn<(typeof platformQuotaRows)[number]>[]>(() => [
  { accessorKey: "platform", header: t("admin.settings.platformQuota.platform") },
  { id: "daily", header: t("admin.settings.platformQuota.daily") },
  { id: "weekly", header: t("admin.settings.platformQuota.weekly") },
  { id: "monthly", header: t("admin.settings.platformQuota.monthly") },
]);

const { copyToClipboard } = useClipboard();

const loading = ref(true);
const loadFailed = ref(false);

// The section DOM is behind the initial settings request. Retry once the form
// becomes visible so a Ctrl+K deep link never loses its highlight on first load.
watch(loading, (ready) => {
  if (!ready) applySettingsDeepLink();
});
const saving = ref(false);
const loadedSettingsSnapshot = ref<Record<string, unknown>>({});
const loadedSettingsPayload = ref<Record<string, unknown>>({});
const testingSmtp = ref(false);
const sendingTestEmail = ref(false);
const smtpPasswordManuallyEdited = ref(false);
const testEmailAddress = ref("");
const registrationEmailSuffixWhitelistTags = ref<string[]>([]);
const registrationEmailSuffixWhitelistDraft = ref("");
const forwardedClientIpHeaderDraft = ref("");
const tablePageSizeOptionsInput = ref("10, 20, 50, 100");

// Admin API Key 状态
const adminApiKeyLoading = ref(true);
const adminApiKeyExists = ref(false);
const adminApiKeyMasked = ref("");
const adminApiKeyOperating = ref(false);
const newAdminApiKey = ref("");
const subscriptionGroups = ref<AdminGroup[]>([]);

// Upstream billing probe state
const upstreamBillingProbeLoading = ref(true);
const upstreamBillingProbeSaving = ref(false);
const upstreamBillingProbeForm = reactive({
  enabled: true,
  interval_minutes: 30,
});

const ollamaCloudUsageLoading = ref(true);
const ollamaCloudUsageSaving = ref(false);
const ollamaCloudUsageForm = reactive({
  enabled: false,
  interval_minutes: 60,
  debounce_minutes: 1,
});

// Overload Cooldown (529) 状态
const overloadCooldownLoading = ref(true);
const overloadCooldownSaving = ref(false);
const overloadCooldownForm = reactive({
  enabled: true,
  cooldown_minutes: 10,
});

// Rate Limit Cooldown (429) 状态
const rateLimit429CooldownLoading = ref(true);
const rateLimit429CooldownSaving = ref(false);
const rateLimit429CooldownForm = reactive({
  enabled: true,
  cooldown_seconds: 5,
});

// Panel API Rate Limit 状态
const panelRateLimitLoading = ref(true);
const panelRateLimitSaving = ref(false);
const panelRateLimitForm = reactive({
  enabled: true,
  user_rpm: 240,
  heavy_rpm: 60,
  exempt_admin: true,
  public_ip_rpm: 300,
});

// Stream Timeout 状态
const streamTimeoutLoading = ref(true);
const streamTimeoutSaving = ref(false);
const streamTimeoutForm = reactive({
  enabled: true,
  action: "temp_unsched" as "temp_unsched" | "error" | "none",
  temp_unsched_minutes: 5,
  threshold_count: 3,
  threshold_window_minutes: 10,
});

// Rectifier 状态
const rectifierLoading = ref(true);
const rectifierSaving = ref(false);
const rectifierForm = reactive({
  enabled: true,
  thinking_signature_enabled: true,
  thinking_budget_enabled: true,
  apikey_signature_enabled: false,
  apikey_signature_patterns: [] as string[],
});

// Beta Policy 状态
const betaPolicyLoading = ref(true);
const betaPolicySaving = ref(false);
const betaPolicyForm = reactive({
  rules: [] as Array<{
    beta_token: string;
    action: "pass" | "filter" | "block";
    scope: "all" | "oauth" | "apikey" | "bedrock";
    error_message?: string;
    model_whitelist?: string[];
    fallback_action?: "pass" | "filter" | "block";
    fallback_error_message?: string;
  }>,
});

// OpenAI Fast/Flex Policy 状态
const openaiFastPolicyForm = reactive({
  rules: [] as OpenAIFastPolicyRule[],
});
// 标记 openai_fast_policy_settings 是否已成功从后端加载，
// 避免后端 GET 出错或字段缺失时，保存把默认规则覆盖成空数组。
const openaiFastPolicyLoaded = ref(false);

const tablePageSizeMin = 5;
const tablePageSizeMax = 1000;
const tablePageSizeDefault = 20;

function defaultLoginAgreementDocuments(): LoginAgreementDocument[] {
  const locale = isZhLocale.value ? "zh" : "en";
  return bundledLegalDocuments(locale).map((doc) => ({
    id: doc.id,
    title: doc.title,
    content_md: "",
  }));
}

function normalizeLoginAgreementDocumentId(raw: string): string {
  return raw
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/[-_]{2,}/g, "-")
    .replace(/^[-_]+|[-_]+$/g, "");
}

function loginAgreementRoutePath(
  doc: LoginAgreementDocument,
  index: number,
): string {
  const id =
    normalizeLoginAgreementDocumentId(doc.id || doc.title) || `doc-${index + 1}`;
  return `/legal/${id}`;
}

type ClaudeOAuthSystemPromptPreset =
  | "billing"
  | "system"
  | "expansion"
  | "custom";

interface ClaudeOAuthSystemPromptBlock {
  id: string;
  enabled: boolean;
  expanded: boolean;
  type: "text";
  preset: ClaudeOAuthSystemPromptPreset;
  text: string;
  cacheControlEnabled: boolean;
  cacheControlTTL: string;
}

interface ClaudeOAuthSystemPromptRawBlock {
  enabled?: boolean;
  type?: string;
  text?: string;
  cache_control?: unknown;
}

const defaultClaudeCodeSystemPrompt =
  "You are Claude Code, Anthropic's official CLI for Claude.";

const defaultClaudeCodeExpansionPrompt = `You are an interactive agent that helps users with software engineering tasks. Use the instructions below and the tools available to you to assist the user.

IMPORTANT: Assist with authorized security testing, defensive security, CTF challenges, and educational contexts. Refuse requests for destructive techniques, DoS attacks, mass targeting, supply chain compromise, or detection evasion for malicious purposes. Dual-use security tools (C2 frameworks, credential testing, exploit development) require clear authorization context: pentesting engagements, CTF competitions, security research, or defensive use cases.
IMPORTANT: You must NEVER generate or guess URLs for the user unless you are confident that the URLs are for helping the user with programming. You may use URLs provided by the user in their messages or local files.

# Tone and style
 - Only use emojis if the user explicitly requests it. Avoid using emojis in all communication unless asked.
 - Your responses should be short and concise.
 - When referencing specific functions or pieces of code include the pattern file_path:line_number to allow the user to easily navigate to the source code location.
 - When referencing GitHub issues or pull requests, use the owner/repo#123 format (e.g. anthropics/claude-code#100) so they render as clickable links.
 - Do not use a colon before tool calls. Your tool calls may not be shown directly in the output, so text like "Let me read the file:" followed by a read tool call should just be "Let me read the file." with a period.`;

let claudeOAuthSystemPromptBlockID = 0;

function nextClaudeOAuthSystemPromptBlockID(): string {
  claudeOAuthSystemPromptBlockID += 1;
  return `claude-oauth-system-prompt-block-${claudeOAuthSystemPromptBlockID}`;
}

function normalizeClaudeOAuthSystemPromptCacheTTL(value: unknown): string {
  return typeof value === "string" && value.trim() ? value.trim() : "5m";
}

function detectClaudeOAuthSystemPromptPreset(
  text: string,
): ClaudeOAuthSystemPromptPreset {
  const trimmed = text.trim();
  if (trimmed === "{billing_header}") {
    return "billing";
  }
  if (
    trimmed === "{claude_code_system_prompt}" ||
    trimmed === defaultClaudeCodeSystemPrompt
  ) {
    return "system";
  }
  if (
    trimmed === "{claude_code_expansion_prompt}" ||
    trimmed === defaultClaudeCodeExpansionPrompt
  ) {
    return "expansion";
  }
  return "custom";
}

function normalizeClaudeOAuthSystemPromptBlockText(
  text: string,
  expansionPrompt = "",
): string {
  const trimmed = text.trim();
  if (trimmed === "{claude_code_system_prompt}") {
    return defaultClaudeCodeSystemPrompt;
  }
  if (trimmed === "{claude_code_expansion_prompt}") {
    return expansionPrompt.trim() || defaultClaudeCodeExpansionPrompt;
  }
  return text;
}

function createClaudeOAuthSystemPromptBlock(
  overrides: Partial<ClaudeOAuthSystemPromptBlock> = {},
): ClaudeOAuthSystemPromptBlock {
  const text = overrides.text ?? "";
  return {
    id: nextClaudeOAuthSystemPromptBlockID(),
    enabled: overrides.enabled ?? true,
    expanded: overrides.expanded ?? true,
    type: "text",
    preset: overrides.preset ?? detectClaudeOAuthSystemPromptPreset(text),
    text,
    cacheControlEnabled: overrides.cacheControlEnabled ?? false,
    cacheControlTTL: overrides.cacheControlTTL ?? "5m",
  };
}

function createDefaultClaudeOAuthSystemPromptBlocks(
  expansionPrompt = "",
): ClaudeOAuthSystemPromptBlock[] {
  const normalizedExpansionPrompt = expansionPrompt.trim();
  const expansionText =
    normalizedExpansionPrompt || defaultClaudeCodeExpansionPrompt;

  return [
    createClaudeOAuthSystemPromptBlock({
      preset: "billing",
      text: "{billing_header}",
    }),
    createClaudeOAuthSystemPromptBlock({
      preset: "system",
      text: defaultClaudeCodeSystemPrompt,
    }),
    createClaudeOAuthSystemPromptBlock({
      preset:
        expansionText === defaultClaudeCodeExpansionPrompt
          ? "expansion"
          : "custom",
      text: expansionText,
      cacheControlEnabled: true,
      cacheControlTTL: "5m",
    }),
  ];
}

function parseClaudeOAuthSystemPromptCacheControl(cacheControl: unknown): {
  enabled: boolean;
  ttl: string;
} {
  if (cacheControl === true) {
    return { enabled: true, ttl: "5m" };
  }
  if (
    cacheControl &&
    typeof cacheControl === "object" &&
    !Array.isArray(cacheControl)
  ) {
    return {
      enabled: true,
      ttl: normalizeClaudeOAuthSystemPromptCacheTTL(
        (cacheControl as Record<string, unknown>).ttl,
      ),
    };
  }
  return { enabled: false, ttl: "5m" };
}

function parseClaudeOAuthSystemPromptBlocks(
  raw: string,
  expansionPrompt = "",
): ClaudeOAuthSystemPromptBlock[] {
  const trimmed = raw.trim();
  if (!trimmed) {
    return createDefaultClaudeOAuthSystemPromptBlocks(expansionPrompt);
  }

  try {
    const parsed = JSON.parse(trimmed) as
      | ClaudeOAuthSystemPromptRawBlock[]
      | { blocks?: ClaudeOAuthSystemPromptRawBlock[] };
    const rawBlocks = Array.isArray(parsed)
      ? parsed
      : Array.isArray(parsed.blocks)
        ? parsed.blocks
        : [];

    if (rawBlocks.length === 0) {
      return createDefaultClaudeOAuthSystemPromptBlocks(expansionPrompt);
    }

    return rawBlocks.map((block) => {
      const cacheControl = parseClaudeOAuthSystemPromptCacheControl(
        block.cache_control,
      );
      const text = normalizeClaudeOAuthSystemPromptBlockText(
        typeof block.text === "string" ? block.text : "",
        expansionPrompt,
      );
      return createClaudeOAuthSystemPromptBlock({
        enabled: block.enabled !== false,
        type: "text",
        text,
        preset: detectClaudeOAuthSystemPromptPreset(text),
        cacheControlEnabled: cacheControl.enabled,
        cacheControlTTL: cacheControl.ttl,
      });
    });
  } catch (_error) {
    return createDefaultClaudeOAuthSystemPromptBlocks(expansionPrompt);
  }
}

function serializeClaudeOAuthSystemPromptBlocksToJSON(
  blocks: ClaudeOAuthSystemPromptBlock[],
): string {
  const source =
    blocks.length > 0
      ? blocks
      : [
          createClaudeOAuthSystemPromptBlock({
            enabled: false,
            preset: "custom",
            text: "",
          }),
        ];

  const rawBlocks = source.map((block) => {
    const raw: ClaudeOAuthSystemPromptRawBlock = {
      enabled: block.enabled,
      type: block.type || "text",
      text: block.text,
    };
    if (block.cacheControlEnabled) {
      raw.cache_control = {
        type: "ephemeral",
        ttl: normalizeClaudeOAuthSystemPromptCacheTTL(block.cacheControlTTL),
      };
    }
    return raw;
  });

  return JSON.stringify(rawBlocks, null, 2);
}

const defaultClaudeOAuthSystemPromptBlocks =
  serializeClaudeOAuthSystemPromptBlocksToJSON(
    createDefaultClaudeOAuthSystemPromptBlocks(),
  );

const claudeOAuthSystemPromptBlocks = ref<ClaudeOAuthSystemPromptBlock[]>(
  createDefaultClaudeOAuthSystemPromptBlocks(),
);

const claudeOAuthSystemPromptPresetOptions = computed(() => [
  {
    value: "billing",
    label: t("admin.settings.gatewayForwarding.systemBlockPresetBilling"),
  },
  {
    value: "system",
    label: t("admin.settings.gatewayForwarding.systemBlockPresetIdentity"),
  },
  {
    value: "expansion",
    label: t("admin.settings.gatewayForwarding.systemBlockPresetExpansion"),
  },
  {
    value: "custom",
    label: t("admin.settings.gatewayForwarding.systemBlockPresetCustom"),
  },
]);

const claudeOAuthSystemPromptBlockTypeOptions = computed(() => [
  {
    value: "text",
    label: t("admin.settings.gatewayForwarding.systemBlockTypeText"),
  },
]);

const claudeOAuthSystemPromptCacheTTLOptions = computed(() => [
  { value: "5m", label: t("admin.settings.gatewayForwarding.cacheTTL5m") },
  { value: "1h", label: t("admin.settings.gatewayForwarding.cacheTTL1h") },
]);

function getClaudeOAuthPresetLabel(
  preset: ClaudeOAuthSystemPromptPreset,
): string {
  return (
    claudeOAuthSystemPromptPresetOptions.value.find(
      (option) => option.value === preset,
    )?.label || t("admin.settings.gatewayForwarding.systemBlockPresetCustom")
  );
}

function syncClaudeOAuthSystemPromptBlocksFormField(): void {
  form.claude_oauth_system_prompt_blocks =
    serializeClaudeOAuthSystemPromptBlocksToJSON(
      claudeOAuthSystemPromptBlocks.value,
    );
}

function addClaudeOAuthSystemPromptBlock(): void {
  claudeOAuthSystemPromptBlocks.value.push(
    createClaudeOAuthSystemPromptBlock({
      expanded: true,
      preset: "custom",
      text: "",
    }),
  );
  syncClaudeOAuthSystemPromptBlocksFormField();
}

function toggleClaudeOAuthSystemPromptBlock(index: number): void {
  const block = claudeOAuthSystemPromptBlocks.value[index];
  if (!block) {
    return;
  }
  block.expanded = !block.expanded;
}

function removeClaudeOAuthSystemPromptBlock(index: number): void {
  claudeOAuthSystemPromptBlocks.value.splice(index, 1);
  syncClaudeOAuthSystemPromptBlocksFormField();
}

function moveClaudeOAuthSystemPromptBlock(
  index: number,
  direction: -1 | 1,
): void {
  const targetIndex = index + direction;
  if (
    targetIndex < 0 ||
    targetIndex >= claudeOAuthSystemPromptBlocks.value.length
  ) {
    return;
  }
  const blocks = claudeOAuthSystemPromptBlocks.value;
  const current = blocks[index];
  blocks[index] = blocks[targetIndex];
  blocks[targetIndex] = current;
  syncClaudeOAuthSystemPromptBlocksFormField();
}

function applyClaudeOAuthSystemPromptPreset(
  index: number,
  value: string | number | boolean | null,
): void {
  const block = claudeOAuthSystemPromptBlocks.value[index];
  if (!block) {
    return;
  }
  const preset = String(value || "custom") as ClaudeOAuthSystemPromptPreset;
  block.preset = preset;
  block.type = "text";
  if (preset === "billing") {
    block.text = "{billing_header}";
    block.cacheControlEnabled = false;
    block.cacheControlTTL = "5m";
  } else if (preset === "system") {
    block.text = defaultClaudeCodeSystemPrompt;
    block.cacheControlEnabled = false;
    block.cacheControlTTL = "5m";
  } else if (preset === "expansion") {
    block.text =
      form.claude_oauth_system_prompt.trim() ||
      defaultClaudeCodeExpansionPrompt;
    block.cacheControlEnabled = true;
    block.cacheControlTTL = "5m";
  }
  syncClaudeOAuthSystemPromptBlocksFormField();
}

function markClaudeOAuthSystemPromptBlockCustom(
  block: ClaudeOAuthSystemPromptBlock,
): void {
  block.preset = detectClaudeOAuthSystemPromptPreset(block.text);
  syncClaudeOAuthSystemPromptBlocksFormField();
}

function resetClaudeOAuthSystemPromptBlocks(): void {
  claudeOAuthSystemPromptBlocks.value = createDefaultClaudeOAuthSystemPromptBlocks(
    form.claude_oauth_system_prompt,
  );
  syncClaudeOAuthSystemPromptBlocksFormField();
}


interface DefaultSubscriptionGroupOption {
  value: number;
  label: string;
  description: string | null;
  platform: AdminGroup["platform"];
  subscriptionType: AdminGroup["subscription_type"];
  rate: number;
  [key: string]: unknown;
}

type SettingsForm = Omit<
  SystemSettings,
  | "wechat_connect_open_enabled"
  | "wechat_connect_mp_enabled"
  | "wechat_connect_mobile_enabled"
> & {
  /** Form always binds a concrete boolean (SystemSettings marks this optional). */
  channel_monitor_hide_throughput: boolean;
  channel_monitor_show_quota: boolean;
  channel_monitor_hide_user_ranking: boolean;
  payment_recharge_center_enabled: boolean;
  smtp_password: string;
  turnstile_secret_key: string;
  tencent_captcha_app_secret_key: string;
  tencent_captcha_cloud_secret_id: string;
  tencent_captcha_cloud_secret_key: string;
  aliyun_captcha_access_key_secret: string;
  linuxdo_connect_client_secret: string;
  dingtalk_connect_client_secret: string;
  wechat_connect_app_secret: string;
  wechat_connect_open_app_secret: string;
  wechat_connect_mp_app_secret: string;
  wechat_connect_mobile_app_secret: string;
  wechat_connect_open_enabled: boolean;
  wechat_connect_mp_enabled: boolean;
  wechat_connect_mobile_enabled: boolean;
  oidc_connect_client_secret: string;
  github_oauth_client_secret: string;
  google_oauth_client_secret: string;
  force_email_on_third_party_signup: boolean;
  openai_low_upstream_rate_priority_enabled: boolean;
  openai_oauth_scheduling_rate_multiplier: number;
  codex_quota_overdraft_enabled: boolean;
  codex_quota_overdraft_business_injection_enabled: boolean;
  openai_advanced_scheduler_enabled: boolean;
  openai_advanced_scheduler_sticky_weighted_enabled: boolean;
  openai_advanced_scheduler_subscription_priority_enabled: boolean;
  openai_advanced_scheduler_lb_top_k: string;
  openai_advanced_scheduler_weight_priority: string;
  openai_advanced_scheduler_weight_load: string;
  openai_advanced_scheduler_weight_queue: string;
  openai_advanced_scheduler_weight_error_rate: string;
  openai_advanced_scheduler_weight_ttft: string;
  openai_advanced_scheduler_weight_reset: string;
  openai_advanced_scheduler_weight_quota_headroom: string;
  openai_advanced_scheduler_weight_upstream_cost: string;
  openai_advanced_scheduler_weight_previous_response: string;
  openai_advanced_scheduler_weight_session_sticky: string;
  // 系统全局平台限额 map；form 内始终归一化为全 4 平台对象（模板非空绑定依赖此不变量）
  default_platform_quotas: DefaultPlatformQuotasMap;
  account_scheduling_thresholds: ReturnType<typeof normalizeAccountSchedulingThresholdsMap>;
};

const schedulingThresholdPlatforms = SCHEDULING_THRESHOLD_PLATFORMS;

const form = reactive<SettingsForm>({
  registration_enabled: true,
  email_verify_enabled: false,
  registration_email_suffix_whitelist: [],
  registration_email_domain_quota_enabled: false,
  registration_email_alias_block_enabled: false,
  promo_code_enabled: true,
  invitation_code_enabled: false,
  password_reset_enabled: false,
  totp_enabled: false,
  totp_encryption_key_configured: false,
  passkey_enabled: false,
  passkey_configured: false,
  passkey_rp_id: "",
  passkey_rp_origins: [],
  session_binding_enabled: false,
  step_up_enabled: false,
  audit_log_retention_days: 180,
  login_agreement_enabled: false,
  login_agreement_mode: "modal",
  login_agreement_updated_at: "2026-03-31",
  login_agreement_documents: defaultLoginAgreementDocuments(),
  default_balance: 0,
  default_platform_quotas: normalizePlatformQuotasMap() as DefaultPlatformQuotasMap,
  account_scheduling_thresholds: normalizeAccountSchedulingThresholdsMap(),
  affiliate_rebate_rate: 20,
  affiliate_rebate_freeze_hours: 0,
  affiliate_rebate_duration_days: 0,
  affiliate_rebate_per_invitee_cap: 0,
  affiliate_admin_recharge_enabled: false,
  default_concurrency: 1,
  default_subscriptions: [],
  force_email_on_third_party_signup: false,
  default_user_rpm_limit: 0,
  site_name: "Sub2API",
  site_logo: "",
  site_subtitle: "Subscription to API Conversion Platform",
  api_base_url: "",
  contact_info: "",
  doc_url: "",
  home_content: "",
  compact_home_enabled: false,
  backend_mode_enabled: false,
  hide_ccs_import_button: false,
  payment_enabled: false,
  payment_recharge_center_enabled: false,
  risk_control_enabled: false,
  cyber_session_block_enabled: false,
  cyber_session_block_ttl_seconds: 3600,
  payment_min_amount: 1,
  payment_max_amount: 10000,
  payment_daily_limit: 50000,
  payment_max_pending_orders: 3,
  payment_order_timeout_minutes: 30,
  payment_balance_disabled: false,
  payment_balance_recharge_multiplier: 1,
  payment_subscription_usd_to_cny_rate: 0,
  payment_recharge_fee_rate: 0,
  payment_enabled_types: [],
  payment_help_image_url: "",
  payment_help_text: "",
  payment_product_name_prefix: "",
  payment_product_name_suffix: "",
  payment_load_balance_strategy: "round-robin",
  payment_cancel_rate_limit_enabled: false,
  payment_cancel_rate_limit_max: 10,
  payment_cancel_rate_limit_window: 1,
  payment_cancel_rate_limit_unit: "day",
  payment_cancel_rate_limit_window_mode: "rolling",
  payment_alipay_force_qrcode: false,
  payment_alipay_mobile_precreate_deep_link: false,
  table_default_page_size: tablePageSizeDefault,
  table_page_size_options: [10, 20, 50, 100],
  custom_menu_items: [] as Array<{
    id: string;
    label: string;
    icon_svg: string;
    url: string;
    visibility: "user" | "admin";
    sort_order: number;
    hide_open_button?: boolean;
  }>,
  custom_endpoints: [] as Array<{
    name: string;
    endpoint: string;
    description: string;
    min_recharge: number;
  }>,
  frontend_url: "",
  ticket_admin_email: "",
  smtp_host: "",
  smtp_port: 587,
  smtp_username: "",
  smtp_password: "",
  smtp_password_configured: false,
  smtp_from_email: "",
  smtp_from_name: "",
  smtp_use_tls: true,
  // Cloudflare Turnstile
  turnstile_enabled: false,
  turnstile_site_key: "",
  turnstile_secret_key: "",
  turnstile_secret_key_configured: false,
  tencent_captcha_enabled: false,
  tencent_captcha_app_id: "",
  tencent_captcha_app_secret_key: "",
  tencent_captcha_app_secret_key_configured: false,
  tencent_captcha_cloud_secret_id: "",
  tencent_captcha_cloud_secret_id_configured: false,
  tencent_captcha_cloud_secret_key: "",
  tencent_captcha_cloud_secret_key_configured: false,
  tencent_captcha_region: "cn",
  aliyun_captcha_enabled: false,
  aliyun_captcha_access_key_id: "",
  aliyun_captcha_access_key_secret: "",
  aliyun_captcha_access_key_secret_configured: false,
  aliyun_captcha_scene_id: "",
  aliyun_captcha_prefix: "",
  aliyun_captcha_region: "cn",
  api_key_acl_trust_forwarded_ip: true,
  forwarded_client_ip_headers: [],
  // LinuxDo Connect OAuth 登录
  linuxdo_connect_enabled: false,
  linuxdo_connect_client_id: "",
  linuxdo_connect_client_secret: "",
  linuxdo_connect_client_secret_configured: false,
  linuxdo_connect_redirect_url: "",
  // DingTalk Connect OAuth 登录
  dingtalk_connect_enabled: false,
  dingtalk_connect_client_id: "",
  dingtalk_connect_client_secret: "",
  dingtalk_connect_client_secret_configured: false,
  dingtalk_connect_redirect_url: "",
  dingtalk_connect_corp_restriction_policy: "none",
  dingtalk_connect_internal_corp_id: "",
  dingtalk_connect_bypass_registration: false,
  dingtalk_connect_sync_corp_email: false,
  dingtalk_connect_sync_display_name: false,
  dingtalk_connect_sync_dept: false,
  dingtalk_connect_sync_corp_email_attr_key: "dingtalk_email",
  dingtalk_connect_sync_display_name_attr_key: "dingtalk_name",
  dingtalk_connect_sync_dept_attr_key: "dingtalk_department",
  dingtalk_connect_sync_corp_email_attr_name: localText("钉钉企业邮箱", "DingTalk Corporate Email"),
  dingtalk_connect_sync_display_name_attr_name: localText("钉钉姓名", "DingTalk Name"),
  dingtalk_connect_sync_dept_attr_name: localText("钉钉部门", "DingTalk Department"),
  wechat_connect_enabled: false,
  wechat_connect_app_id: "",
  wechat_connect_app_secret: "",
  wechat_connect_app_secret_configured: false,
  wechat_connect_open_app_id: "",
  wechat_connect_open_app_secret: "",
  wechat_connect_open_app_secret_configured: false,
  wechat_connect_mp_app_id: "",
  wechat_connect_mp_app_secret: "",
  wechat_connect_mp_app_secret_configured: false,
  wechat_connect_mobile_app_id: "",
  wechat_connect_mobile_app_secret: "",
  wechat_connect_mobile_app_secret_configured: false,
  wechat_connect_open_enabled: false,
  wechat_connect_mp_enabled: false,
  wechat_connect_mobile_enabled: false,
  wechat_connect_mode: "open",
  wechat_connect_scopes: "snsapi_login",
  wechat_connect_redirect_url: "",
  wechat_connect_frontend_redirect_url: "/auth/wechat/callback",
  // Generic OIDC OAuth 登录
  oidc_connect_enabled: false,
  oidc_connect_provider_name: "OIDC",
  oidc_connect_client_id: "",
  oidc_connect_client_secret: "",
  oidc_connect_client_secret_configured: false,
  oidc_connect_issuer_url: "",
  oidc_connect_discovery_url: "",
  oidc_connect_authorize_url: "",
  oidc_connect_token_url: "",
  oidc_connect_userinfo_url: "",
  oidc_connect_jwks_url: "",
  oidc_connect_scopes: "openid email profile",
  oidc_connect_redirect_url: "",
  oidc_connect_frontend_redirect_url: "/auth/oidc/callback",
  oidc_connect_token_auth_method: "client_secret_post",
  oidc_connect_use_pkce: false,
  oidc_connect_validate_id_token: false,
  oidc_connect_allowed_signing_algs: "RS256,ES256,PS256",
  oidc_connect_clock_skew_seconds: 120,
  oidc_connect_require_email_verified: false,
  oidc_connect_userinfo_email_path: "",
  oidc_connect_userinfo_id_path: "",
  oidc_connect_userinfo_username_path: "",
  // GitHub / Google 邮箱快捷登录
  github_oauth_enabled: false,
  github_oauth_client_id: "",
  github_oauth_client_secret: "",
  github_oauth_client_secret_configured: false,
  github_oauth_redirect_url: "",
  github_oauth_frontend_redirect_url: "/auth/oauth/callback",
  google_oauth_enabled: false,
  google_oauth_client_id: "",
  google_oauth_client_secret: "",
  google_oauth_client_secret_configured: false,
  google_oauth_redirect_url: "",
  google_oauth_frontend_redirect_url: "/auth/oauth/callback",
  // Model fallback
  enable_model_fallback: false,
  fallback_model_anthropic: "claude-3-5-sonnet-20241022",
  fallback_model_openai: "gpt-4o",
  fallback_model_gemini: "gemini-2.5-pro",
  fallback_model_antigravity: "gemini-2.5-pro",
  grok_default_text_model: "grok-4.5",
  grok_cross_client_model_map_enabled: false,
  grok_default_base_url_mode: "cli",
  // Identity patch (Claude -> Gemini)
  enable_identity_patch: true,
  identity_patch_prompt: "",
  // Ops monitoring (vNext)
  ops_monitoring_enabled: true,
  ops_realtime_monitoring_enabled: true,
  ops_query_mode_default: "auto",
  ops_metrics_interval_seconds: 60,
  // Claude Code version check
  min_claude_code_version: "",
  max_claude_code_version: "",
  // 分组隔离
  allow_ungrouped_key_scheduling: false,
  openai_low_upstream_rate_priority_enabled: false,
  openai_oauth_scheduling_rate_multiplier: 1,
  // Codex 5h/7d detector stays on for compatibility; real-request injection
  // is opt-in because upstream may count the synthetic tool pair.
  codex_quota_overdraft_enabled: true,
  codex_quota_overdraft_business_injection_enabled: false,
  openai_advanced_scheduler_enabled: false,
  openai_advanced_scheduler_sticky_weighted_enabled: false,
  openai_advanced_scheduler_subscription_priority_enabled: false,
  openai_advanced_scheduler_lb_top_k: "",
  openai_advanced_scheduler_weight_priority: "",
  openai_advanced_scheduler_weight_load: "",
  openai_advanced_scheduler_weight_queue: "",
  openai_advanced_scheduler_weight_error_rate: "",
  openai_advanced_scheduler_weight_ttft: "",
  openai_advanced_scheduler_weight_reset: "",
  openai_advanced_scheduler_weight_quota_headroom: "",
  openai_advanced_scheduler_weight_upstream_cost: "",
  openai_advanced_scheduler_weight_previous_response: "",
  openai_advanced_scheduler_weight_session_sticky: "",
  // Gateway forwarding behavior
  openai_ttft_mode: "semantic",
  enable_fingerprint_unification: true,
  enable_metadata_passthrough: false,
  enable_cch_signing: false,
  enable_claude_oauth_system_prompt_injection: true,
  claude_oauth_system_prompt: "",
  claude_oauth_system_prompt_blocks: defaultClaudeOAuthSystemPromptBlocks,
  enable_anthropic_cache_ttl_1h_injection: false,
  // 两套预扣费系统运行时开关，默认都关
  billing_balance_preauthorization_enabled: false,
  billing_inflight_reservation_enabled: false,
  rewrite_message_cache_control: false,
  enable_client_dateline_normalization: true,
  antigravity_user_agent_version: "",
  openai_codex_user_agent: "",
  openai_codex_client_version: "",
  // 只读展示：自动同步任务写入的官方最新稳定版，不参与提交（提交载荷按字段显式构造）
  openai_codex_client_version_synced: "",
  openai_codex_version_auto_sync_enabled: true,
  openai_codex_ticket_enabled: false,
  openai_codex_ticket_harvest_proxy_url: "",
  openai_codex_ticket_harvest_proxy_configured: false,
  // codex_cli_only 加固
  min_codex_version: "",
  max_codex_version: "",
  codex_cli_only_blacklist: "",
  codex_cli_only_whitelist: "",
  codex_cli_only_allow_app_server_clients: false,
  codex_cli_only_engine_fingerprint_signals: "",
  // 余额、订阅到期与账号限额通知
  balance_low_notify_enabled: false,
  balance_low_notify_threshold: 0,
  balance_low_notify_recharge_url: "",
  subscription_expiry_notify_enabled: true,
  account_quota_notify_enabled: false,
  account_quota_notify_emails: [] as NotifyEmailEntry[],
  // Channel Monitor feature switch
  channel_monitor_enabled: true,
  channel_monitor_mode: 'v1' as 'v1' | 'v2',
  channel_monitor_default_interval_seconds: 60,
  channel_monitor_hide_throughput: false,
  channel_monitor_show_quota: false,
  channel_monitor_hide_user_ranking: false,
  // Available Channels feature switch
  available_channels_enabled: false,
  // Model Plaza feature switches + description
  model_plaza_enabled: false,
  model_plaza_require_auth: false,
  model_plaza_description: '',
  // Plugin management menu visibility; plugin runtime is unaffected.
  plugin_management_enabled: false,
  // Affiliate (邀请返利) feature switch
  affiliate_enabled: false,
  // Allow user view error requests
  allow_user_view_error_requests: false,
});

// 人机验证 UI 状态：单卡片「总开关 + 服务商单选」，落库仍是三个独立
// enabled 键（与上游一致），由下面的映射保证同一时间至多一家启用。
type CaptchaProviderSelection = "turnstile" | "tencent" | "aliyun";

const captchaProviderSelection = ref<CaptchaProviderSelection>("turnstile");

function applyCaptchaSelection(provider: CaptchaProviderSelection | null): void {
  form.turnstile_enabled = provider === "turnstile";
  form.tencent_captcha_enabled = provider === "tencent";
  form.aliyun_captcha_enabled = provider === "aliyun";
}

const captchaMasterEnabled = computed({
  get: () =>
    form.turnstile_enabled ||
    form.tencent_captcha_enabled ||
    form.aliyun_captcha_enabled,
  set: (enabled: boolean) =>
    applyCaptchaSelection(enabled ? captchaProviderSelection.value : null),
});

function selectCaptchaProvider(provider: CaptchaProviderSelection): void {
  captchaProviderSelection.value = provider;
  applyCaptchaSelection(provider);
}

// 天御中国站与国际站是两套独立账号体系，控制台与文档入口不通用，
// 按当前选择的站点给出对应链接，避免管理员在错误的控制台里找不到 CaptchaAppId。
const tencentCaptchaLinks = computed(() =>
  form.tencent_captcha_region === "intl"
    ? {
        console: "https://console.tencentcloud.com/captcha/graphical",
        cloudKeys: "https://console.tencentcloud.com/cam/capi",
        webDocs: "https://www.tencentcloud.com/document/product/1159/49680",
      }
    : {
        console: "https://console.cloud.tencent.com/captcha",
        cloudKeys: "https://console.cloud.tencent.com/cam/capi",
        webDocs: "https://cloud.tencent.com/document/product/1110/36841",
      },
);

function syncCaptchaProviderSelection(): void {
  if (form.tencent_captcha_enabled) {
    captchaProviderSelection.value = "tencent";
  } else if (form.aliyun_captcha_enabled) {
    captchaProviderSelection.value = "aliyun";
  } else if (form.turnstile_enabled) {
    captchaProviderSelection.value = "turnstile";
  }
}

type OpenAIAdvancedSchedulerOverrideKey =
  | "openai_advanced_scheduler_lb_top_k"
  | "openai_advanced_scheduler_weight_priority"
  | "openai_advanced_scheduler_weight_load"
  | "openai_advanced_scheduler_weight_queue"
  | "openai_advanced_scheduler_weight_error_rate"
  | "openai_advanced_scheduler_weight_ttft"
  | "openai_advanced_scheduler_weight_reset"
  | "openai_advanced_scheduler_weight_quota_headroom"
  | "openai_advanced_scheduler_weight_upstream_cost"
  | "openai_advanced_scheduler_weight_previous_response"
  | "openai_advanced_scheduler_weight_session_sticky";

type OpenAIAdvancedSchedulerEffectiveKey =
  | "openai_advanced_scheduler_effective_lb_top_k"
  | "openai_advanced_scheduler_effective_weight_priority"
  | "openai_advanced_scheduler_effective_weight_load"
  | "openai_advanced_scheduler_effective_weight_queue"
  | "openai_advanced_scheduler_effective_weight_error_rate"
  | "openai_advanced_scheduler_effective_weight_ttft"
  | "openai_advanced_scheduler_effective_weight_reset"
  | "openai_advanced_scheduler_effective_weight_quota_headroom"
  | "openai_advanced_scheduler_effective_weight_upstream_cost"
  | "openai_advanced_scheduler_effective_weight_previous_response"
  | "openai_advanced_scheduler_effective_weight_session_sticky";

const openAIAdvancedSchedulerWeightFields = computed<
  Array<{
    key: OpenAIAdvancedSchedulerOverrideKey;
    label: string;
    placeholder: string;
  }>
>(() => {
  const placeholder = (
    effectiveKey: OpenAIAdvancedSchedulerEffectiveKey,
    fallbackValue: string,
  ) => {
    const effectiveValue = String(
      (form as Record<string, unknown>)[effectiveKey] ?? "",
    ).trim();
    return t("admin.settings.openaiExperimentalScheduler.defaultPlaceholder", {
      value: effectiveValue || fallbackValue,
    });
  };

  return [
    {
      key: "openai_advanced_scheduler_lb_top_k",
      label: t("admin.settings.openaiExperimentalScheduler.topKLabel"),
      placeholder: placeholder("openai_advanced_scheduler_effective_lb_top_k", "7"),
    },
    {
      key: "openai_advanced_scheduler_weight_priority",
      label: t("admin.settings.openaiExperimentalScheduler.priorityWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_priority", "1"),
    },
    {
      key: "openai_advanced_scheduler_weight_load",
      label: t("admin.settings.openaiExperimentalScheduler.loadWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_load", "1"),
    },
    {
      key: "openai_advanced_scheduler_weight_queue",
      label: t("admin.settings.openaiExperimentalScheduler.queueWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_queue", "0.7"),
    },
    {
      key: "openai_advanced_scheduler_weight_error_rate",
      label: t("admin.settings.openaiExperimentalScheduler.errorRateWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_error_rate", "0.8"),
    },
    {
      key: "openai_advanced_scheduler_weight_ttft",
      label: t("admin.settings.openaiExperimentalScheduler.ttftWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_ttft", "0.5"),
    },
    {
      key: "openai_advanced_scheduler_weight_reset",
      label: t("admin.settings.openaiExperimentalScheduler.resetWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_reset", "0"),
    },
    {
      key: "openai_advanced_scheduler_weight_quota_headroom",
      label: t("admin.settings.openaiExperimentalScheduler.quotaHeadroomWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_quota_headroom", "0"),
    },
    {
      key: "openai_advanced_scheduler_weight_upstream_cost",
      label: t("admin.settings.openaiExperimentalScheduler.upstreamCostWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_upstream_cost", "0"),
    },
    {
      key: "openai_advanced_scheduler_weight_previous_response",
      label: t("admin.settings.openaiExperimentalScheduler.previousResponseWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_previous_response", "5"),
    },
    {
      key: "openai_advanced_scheduler_weight_session_sticky",
      label: t("admin.settings.openaiExperimentalScheduler.sessionStickyWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_session_sticky", "3"),
    },
  ];
});

const authSourceDefaults = reactive<AuthSourceDefaultsState>(
  buildAuthSourceDefaultsState({}),
);

const authSourceDefaultsMeta = computed(() => [
  {
    source: "email" as AuthSourceType,
    title: t("admin.settings.authSourceDefaults.sources.email.title"),
    description: t("admin.settings.authSourceDefaults.sources.email.description"),
  },
  {
    source: "linuxdo" as AuthSourceType,
    title: t("admin.settings.authSourceDefaults.sources.linuxdo.title"),
    description: t("admin.settings.authSourceDefaults.sources.linuxdo.description"),
  },
  {
    source: "oidc" as AuthSourceType,
    title: t("admin.settings.authSourceDefaults.sources.oidc.title"),
    description: t("admin.settings.authSourceDefaults.sources.oidc.description"),
  },
  {
    source: "wechat" as AuthSourceType,
    title: t("admin.settings.authSourceDefaults.sources.wechat.title"),
    description: t("admin.settings.authSourceDefaults.sources.wechat.description"),
  },
  {
    source: "github" as AuthSourceType,
    title: "GitHub",
    description: localText(
      "通过 GitHub 已验证邮箱首次注册或首次绑定时应用。",
      "Applied on first signup or first bind through a verified GitHub email.",
    ),
  },
  {
    source: "google" as AuthSourceType,
    title: "Google",
    description: localText(
      "通过 Google 已验证邮箱首次注册或首次绑定时应用。",
      "Applied on first signup or first bind through a verified Google email.",
    ),
  },
  {
    source: "dingtalk" as AuthSourceType,
    title: t("auth.dingtalkProviderName"),
    description: localText(
      "通过钉钉首次注册或首次绑定时应用。",
      "Applied on first signup or first bind through DingTalk.",
    ),
  },
]);

// Proxies for web search emulation ProxySelector
const webSearchProxies = ref<Proxy[]>([]);

// Web Search Emulation config (loaded/saved separately)
const DEFAULT_WEB_SEARCH_QUOTA_LIMIT = 1000;

const webSearchConfig = reactive<WebSearchEmulationConfig>({
  enabled: false,
  providers: [],
});
const loadedWebSearchSnapshot = ref('');

function stableSettingsValue(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableSettingsValue).join(',')}]`;
  if (value !== null && typeof value === 'object') {
    const object = value as Record<string, unknown>;
    return `{${Object.keys(object).sort().map((key) => `${JSON.stringify(key)}:${stableSettingsValue(object[key])}`).join(',')}}`;
  }
  return JSON.stringify(value) ?? 'undefined';
}

const expandedProviders = reactive<Record<number, boolean>>({});
const apiKeyVisible = reactive<Record<number, boolean>>({});
const wsTestQuery = ref("");
const wsTestLoading = ref(false);
const wsTestResult = ref<WebSearchTestResult | null>(null);
const wsTestDialogOpen = ref(false);

function openTestDialog() {
  wsTestResult.value = null;
  wsTestDialogOpen.value = true;
}

function toggleProviderExpand(idx: number) {
  expandedProviders[idx] = !expandedProviders[idx];
}

function removeWebSearchProvider(idx: number) {
  webSearchConfig.providers.splice(idx, 1);
  // Re-index expandedProviders and apiKeyVisible after removal
  const newExpanded: Record<number, boolean> = {};
  const newVisible: Record<number, boolean> = {};
  for (let i = 0; i < webSearchConfig.providers.length; i++) {
    const oldIdx = i >= idx ? i + 1 : i;
    newExpanded[i] = expandedProviders[oldIdx] ?? false;
    newVisible[i] = apiKeyVisible[oldIdx] ?? false;
  }
  Object.keys(expandedProviders).forEach(
    (k) => delete expandedProviders[Number(k)],
  );
  Object.keys(apiKeyVisible).forEach((k) => delete apiKeyVisible[Number(k)]);
  Object.assign(expandedProviders, newExpanded);
  Object.assign(apiKeyVisible, newVisible);
}

function addWebSearchProvider() {
  const idx = webSearchConfig.providers.length;
  webSearchConfig.providers.push({
    type: "brave",
    api_key: "",
    api_key_configured: false,
    quota_limit: DEFAULT_WEB_SEARCH_QUOTA_LIMIT,
    subscribed_at: null,
    proxy_id: null,
    expires_at: null,
  } as WebSearchProviderConfig);
  expandedProviders[idx] = true;
}

function formatSubscribedAt(ts: number | null): string {
  if (!ts) return "";
  // Use UTC to avoid timezone drift on repeated edits
  const d = new Date(ts * 1000);
  const y = d.getUTCFullYear();
  const m = String(d.getUTCMonth() + 1).padStart(2, "0");
  const day = String(d.getUTCDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function parseSubscribedAt(dateStr: string): number | null {
  if (!dateStr) return null;
  // Parse as UTC to match formatSubscribedAt
  return Math.floor(new Date(dateStr + "T00:00:00Z").getTime() / 1000);
}

function quotaPercentage(provider: WebSearchProviderConfig): number {
  if (!provider.quota_limit || provider.quota_limit <= 0) return 0;
  return ((provider.quota_used ?? 0) / provider.quota_limit) * 100;
}

async function resetWebSearchUsage(idx: number) {
  const provider = webSearchConfig.providers[idx];
  if (!provider) return;
  if (!confirm(t("admin.settings.webSearchEmulation.resetUsageConfirm")))
    return;
  try {
    await adminAPI.settings.resetWebSearchUsage({
      provider_type: provider.type,
    });
    provider.quota_used = 0;
    appStore.showSuccess(
      t("admin.settings.webSearchEmulation.resetUsageSuccess"),
    );
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  }
}

async function copyApiKey(idx: number) {
  const key = webSearchConfig.providers[idx]?.api_key;
  if (!key) {
    appStore.showError(
      t("admin.settings.webSearchEmulation.apiKeyPlaceholder"),
    );
    return;
  }
  try {
    await navigator.clipboard.writeText(key);
    appStore.showSuccess(t("admin.settings.webSearchEmulation.copied"));
  } catch {
    appStore.showError(t("common.error"));
  }
}

async function testWebSearchProvider() {
  wsTestLoading.value = true;
  wsTestResult.value = null;
  try {
    const query =
      wsTestQuery.value.trim() ||
      t("admin.settings.webSearchEmulation.testDefaultQuery");
    wsTestResult.value = await adminAPI.settings.testWebSearchEmulation(query);
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  } finally {
    wsTestLoading.value = false;
  }
}

async function loadWebSearchConfig() {
  loadedWebSearchSnapshot.value = JSON.stringify({ enabled: false, providers: [] });
  try {
    const [resp, proxiesResp] = await Promise.all([
      adminAPI.settings.getWebSearchEmulationConfig(),
      adminAPI.proxies.list().catch(() => ({ items: [] as Proxy[] })),
    ]);
    if (resp) {
      webSearchConfig.enabled = resp.enabled || false;
      webSearchConfig.providers = resp.providers || [];
      loadedWebSearchSnapshot.value = JSON.stringify({ enabled: webSearchConfig.enabled, providers: webSearchConfig.providers });
    }
    webSearchProxies.value = proxiesResp.items || [];
  } catch (err: unknown) {
    // 404 is expected when config hasn't been created yet; show error for other failures
    const status = (err as { status?: number })?.status;
    if (status !== 404 && status !== undefined) {
      appStore.showError(extractApiErrorMessage(err, t("common.error")));
    }
  }
}

async function saveWebSearchConfig(): Promise<"saved" | "unchanged" | "failed"> {
  try {
    for (const p of webSearchConfig.providers) {
      const raw = p.quota_limit;
      if (raw != null && Number(raw) !== 0 && Number(raw) < 1) {
        appStore.showError(
          t("admin.settings.webSearchEmulation.quotaLimitMustBePositive"),
        );
        return "failed";
      }
    }
    const providers = webSearchConfig.providers.map(
      (p: WebSearchProviderConfig) => ({
        ...p,
        quota_limit: Number(p.quota_limit) > 0 ? Number(p.quota_limit) : null,
      }),
    );
    const nextSnapshot = JSON.stringify({ enabled: webSearchConfig.enabled, providers });
    if (nextSnapshot === loadedWebSearchSnapshot.value) return "unchanged";
    await adminAPI.settings.updateWebSearchEmulationConfig({
      enabled: webSearchConfig.enabled,
      providers,
    });
    loadedWebSearchSnapshot.value = nextSnapshot;
    return "saved";
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
    return "failed";
  }
}

const defaultSubscriptionGroupOptions = computed<
  DefaultSubscriptionGroupOption[]
>(() =>
  subscriptionGroups.value.map((group) => ({
    value: group.id,
    label: group.name,
    description: group.description,
    platform: group.platform,
    subscriptionType: group.subscription_type,
    rate: group.rate_multiplier,
  })),
);

const registrationEmailSuffixWhitelistSeparatorKeys = new Set([
  " ",
  ",",
  "，",
  "Enter",
  "Tab",
]);

function removeRegistrationEmailSuffixWhitelistTag(suffix: string) {
  registrationEmailSuffixWhitelistTags.value =
    registrationEmailSuffixWhitelistTags.value.filter(
      (item) => item !== suffix,
    );
}

function addRegistrationEmailSuffixWhitelistTag(raw: string) {
  const suffix = normalizeRegistrationEmailSuffixDomain(raw);
  if (
    !isRegistrationEmailSuffixDomainValid(suffix) ||
    registrationEmailSuffixWhitelistTags.value.includes(suffix)
  ) {
    return;
  }
  registrationEmailSuffixWhitelistTags.value = [
    ...registrationEmailSuffixWhitelistTags.value,
    suffix,
  ];
}

function commitRegistrationEmailSuffixWhitelistDraft() {
  if (!registrationEmailSuffixWhitelistDraft.value) {
    return;
  }
  addRegistrationEmailSuffixWhitelistTag(
    registrationEmailSuffixWhitelistDraft.value,
  );
  registrationEmailSuffixWhitelistDraft.value = "";
}

function handleRegistrationEmailSuffixWhitelistDraftInput() {
  registrationEmailSuffixWhitelistDraft.value =
    normalizeRegistrationEmailSuffixDomain(
      registrationEmailSuffixWhitelistDraft.value,
    );
}

function handleRegistrationEmailSuffixWhitelistDraftKeydown(
  event: KeyboardEvent,
) {
  if (event.isComposing) {
    return;
  }

  if (registrationEmailSuffixWhitelistSeparatorKeys.has(event.key)) {
    event.preventDefault();
    commitRegistrationEmailSuffixWhitelistDraft();
    return;
  }

  if (
    event.key === "Backspace" &&
    !registrationEmailSuffixWhitelistDraft.value &&
    registrationEmailSuffixWhitelistTags.value.length > 0
  ) {
    registrationEmailSuffixWhitelistTags.value.pop();
  }
}

function handleRegistrationEmailSuffixWhitelistPaste(event: ClipboardEvent) {
  const text = event.clipboardData?.getData("text") || "";
  if (!text.trim()) {
    return;
  }
  event.preventDefault();
  const tokens = parseRegistrationEmailSuffixWhitelistInput(text);
  for (const token of tokens) {
    addRegistrationEmailSuffixWhitelistTag(token);
  }
}

const forwardedClientIpHeaderSeparatorKeys = new Set([
  " ",
  ",",
  "，",
  "Enter",
  "Tab",
]);
const forwardedClientIpHeaderTokenPattern = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;
const maxForwardedClientIpHeaders = 16;

type ForwardedClientIpHeaderResult = "added" | "duplicate" | "invalid" | "full";

function normalizeForwardedClientIpHeader(raw: string): string {
  const header = raw.trim();
  if (!forwardedClientIpHeaderTokenPattern.test(header)) {
    return "";
  }

  return header
    .toLowerCase()
    .split("-")
    .map((part) => `${part.charAt(0).toUpperCase()}${part.slice(1)}`)
    .join("-");
}

function normalizeForwardedClientIpHeaders(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }

  const headers: string[] = [];
  const seen = new Set<string>();
  for (const raw of value) {
    if (typeof raw !== "string") {
      continue;
    }
    const header = normalizeForwardedClientIpHeader(raw);
    const key = header.toLowerCase();
    if (!header || seen.has(key) || headers.length >= maxForwardedClientIpHeaders) {
      continue;
    }
    seen.add(key);
    headers.push(header);
  }
  return headers;
}

function removeForwardedClientIpHeader(header: string) {
  form.forwarded_client_ip_headers = form.forwarded_client_ip_headers.filter(
    (item) => item !== header,
  );
}

function addForwardedClientIpHeader(raw: string): ForwardedClientIpHeaderResult {
  const header = normalizeForwardedClientIpHeader(raw);
  if (!header) {
    return "invalid";
  }
  if (
    form.forwarded_client_ip_headers.some(
      (item) => item.toLowerCase() === header.toLowerCase(),
    )
  ) {
    return "duplicate";
  }
  if (form.forwarded_client_ip_headers.length >= maxForwardedClientIpHeaders) {
    return "full";
  }
  form.forwarded_client_ip_headers = [
    ...form.forwarded_client_ip_headers,
    header,
  ];
  return "added";
}

function showForwardedClientIpHeaderError(result: ForwardedClientIpHeaderResult) {
  if (result === "invalid") {
    appStore.showError(t("admin.settings.apiKeyAcl.forwardedClientIpHeaderInvalid"));
  } else if (result === "full") {
    appStore.showError(
      t("admin.settings.apiKeyAcl.forwardedClientIpHeadersLimit", {
        max: maxForwardedClientIpHeaders,
      }),
    );
  }
}

function commitForwardedClientIpHeaderDraft() {
  const draft = forwardedClientIpHeaderDraft.value;
  if (!draft) {
    return;
  }
  const result = addForwardedClientIpHeader(draft);
  showForwardedClientIpHeaderError(result);
  forwardedClientIpHeaderDraft.value = "";
}

function handleForwardedClientIpHeaderKeydown(event: KeyboardEvent) {
  if (event.isComposing) {
    return;
  }
  if (forwardedClientIpHeaderSeparatorKeys.has(event.key)) {
    event.preventDefault();
    commitForwardedClientIpHeaderDraft();
    return;
  }
  if (
    event.key === "Backspace" &&
    !forwardedClientIpHeaderDraft.value &&
    form.forwarded_client_ip_headers.length > 0
  ) {
    form.forwarded_client_ip_headers.pop();
  }
}

function handleForwardedClientIpHeaderPaste(event: ClipboardEvent) {
  const text = event.clipboardData?.getData("text") || "";
  if (!text.trim()) {
    return;
  }
  event.preventDefault();

  let error: ForwardedClientIpHeaderResult | undefined;
  for (const token of text.split(/[,，;\r\n]+/)) {
    if (!token.trim()) {
      continue;
    }
    const result = addForwardedClientIpHeader(token);
    if (result === "invalid" || result === "full") {
      error = result;
    }
  }
  if (error) {
    showForwardedClientIpHeaderError(error);
  }
}

// Quota notify email helpers
const addQuotaNotifyEmail = () => {
  if (!form.account_quota_notify_emails) {
    form.account_quota_notify_emails = [];
  }
  form.account_quota_notify_emails.push({
    email: "",
    disabled: false,
    verified: true,
  });
};

const currentOrigin =
  typeof window !== "undefined" ? window.location.origin : "";

function buildApiCallbackUrl(path: string): string {
  const base = (form.api_base_url || currentOrigin).replace(/\/+$/, "");
  const apiRoot = base.endsWith("/api/v1") ? base : `${base}/api/v1`;
  return `${apiRoot}${path.startsWith("/") ? path : `/${path}`}`;
}

// LinuxDo OAuth redirect URL suggestion
const linuxdoRedirectUrlSuggestion = computed(() => {
  return buildApiCallbackUrl("/auth/oauth/linuxdo/callback");
});

async function setAndCopyLinuxdoRedirectUrl() {
  const url = linuxdoRedirectUrlSuggestion.value;
  if (!url) return;

  form.linuxdo_connect_redirect_url = url;
  await copyToClipboard(
    url,
    t("admin.settings.linuxdo.redirectUrlSetAndCopied"),
  );
}

type EmailOAuthProvider = "github" | "google";

const githubOAuthRedirectUrlSuggestion = computed(() => {
  return buildApiCallbackUrl("/auth/oauth/github/callback");
});

const googleOAuthRedirectUrlSuggestion = computed(() => {
  return buildApiCallbackUrl("/auth/oauth/google/callback");
});

async function setAndCopyEmailOAuthRedirectUrl(provider: EmailOAuthProvider) {
  const url =
    provider === "github"
      ? githubOAuthRedirectUrlSuggestion.value
      : googleOAuthRedirectUrlSuggestion.value;
  if (!url) return;

  if (provider === "github") {
    form.github_oauth_redirect_url = url;
  } else {
    form.google_oauth_redirect_url = url;
  }
  await copyToClipboard(
    url,
    localText("回调地址已写入并复制。", "Callback URL set and copied."),
  );
}

const wechatRedirectUrlSuggestion = computed(() => {
  return buildApiCallbackUrl("/auth/oauth/wechat/callback");
});

function syncWeChatConnectMode(preferredMode?: WeChatConnectMode) {
  if (form.wechat_connect_mp_enabled && form.wechat_connect_mobile_enabled) {
    if (preferredMode === "mobile") {
      form.wechat_connect_mp_enabled = false;
    } else {
      form.wechat_connect_mobile_enabled = false;
    }
  }

  const capabilities = resolveWeChatConnectModeCapabilities(
    form.wechat_connect_open_enabled,
    form.wechat_connect_mp_enabled,
    form.wechat_connect_mobile_enabled,
    form.wechat_connect_mode,
  );
  form.wechat_connect_open_enabled = capabilities.openEnabled;
  form.wechat_connect_mp_enabled = capabilities.mpEnabled;
  form.wechat_connect_mobile_enabled = capabilities.mobileEnabled;
  form.wechat_connect_mode = deriveWeChatConnectStoredMode(
    capabilities.openEnabled,
    capabilities.mpEnabled,
    capabilities.mobileEnabled,
    form.wechat_connect_mode,
  );
  form.wechat_connect_scopes = defaultWeChatConnectScopesForMode(
    form.wechat_connect_mode,
  );
}

function handleWeChatOpenEnabledChange(value: boolean) {
  form.wechat_connect_open_enabled = value;
  syncWeChatConnectMode(value ? "open" : undefined);
}

function handleWeChatMPEnabledChange(value: boolean) {
  form.wechat_connect_mp_enabled = value;
  if (value) {
    form.wechat_connect_mobile_enabled = false;
  }
  syncWeChatConnectMode(value ? "mp" : undefined);
}

function handleWeChatMobileEnabledChange(value: boolean) {
  form.wechat_connect_mobile_enabled = value;
  if (value) {
    form.wechat_connect_mp_enabled = false;
  }
  syncWeChatConnectMode(value ? "mobile" : undefined);
}

async function setAndCopyWeChatRedirectUrl() {
  const url = wechatRedirectUrlSuggestion.value;
  if (!url) return;

  form.wechat_connect_redirect_url = url;
  await copyToClipboard(
    url,
    t("admin.settings.wechatConnect.redirectUrlSetAndCopied"),
  );
}

const oidcRedirectUrlSuggestion = computed(() => {
  return buildApiCallbackUrl("/auth/oauth/oidc/callback");
});

async function setAndCopyOIDCRedirectUrl() {
  const url = oidcRedirectUrlSuggestion.value;
  if (!url) return;

  form.oidc_connect_redirect_url = url;
  await copyToClipboard(url, t("admin.settings.oidc.redirectUrlSetAndCopied"));
}

// Custom menu item management
function addMenuItem() {
  form.custom_menu_items.push({
    id: "",
    label: "",
    icon_svg: "",
    url: "",
    visibility: "user",
    sort_order: form.custom_menu_items.length,
  });
}

function removeMenuItem(index: number) {
  form.custom_menu_items.splice(index, 1);
  // Re-index sort_order
  form.custom_menu_items.forEach((item, i) => {
    item.sort_order = i;
  });
}

function moveMenuItem(index: number, direction: -1 | 1) {
  const targetIndex = index + direction;
  if (targetIndex < 0 || targetIndex >= form.custom_menu_items.length) return;
  const items = form.custom_menu_items;
  const temp = items[index];
  items[index] = items[targetIndex];
  items[targetIndex] = temp;
  // Re-index sort_order
  items.forEach((item, i) => {
    item.sort_order = i;
  });
}

// Custom endpoint management
function normalizeEndpointThreshold(ep: { min_recharge?: number | null }) {
  const value = Number(ep.min_recharge);
  ep.min_recharge = Number.isFinite(value) && value > 0 ? value : 1000;
}

function addEndpoint() {
  form.custom_endpoints.push({
    name: "",
    endpoint: "",
    description: "",
    min_recharge: 1000,
  });
}

function removeEndpoint(index: number) {
  form.custom_endpoints.splice(index, 1);
}

function addLoginAgreementDocument() {
  form.login_agreement_documents.push({
    id: `custom-${Date.now().toString(36)}`,
    title: "",
    content_md: "",
  });
}

function fillBundledLoginAgreementDocuments() {
  const locale = isZhLocale.value ? "zh" : "en";
  const existing = new Map<string, LoginAgreementDocument>();
  for (const doc of form.login_agreement_documents) {
    const canonical =
      resolveLegalDocumentId(doc.id) ||
      normalizeLoginAgreementDocumentId(doc.id);
    if (canonical) {
      existing.set(canonical, doc);
    }
  }
  for (const bundled of bundledLegalDocuments(locale)) {
    const found = existing.get(bundled.id);
    if (found) {
      if (!found.title.trim()) {
        found.title = bundled.title;
      }
      found.content_md = bundled.content_md;
      continue;
    }
    form.login_agreement_documents.push({
      id: bundled.id,
      title: bundled.title,
      content_md: bundled.content_md,
    });
  }
}

function ensureBundledLoginAgreementDocuments(
  documents: LoginAgreementDocument[],
): LoginAgreementDocument[] {
  const locale = isZhLocale.value ? "zh" : "en";
  const next = documents.map((doc) => ({ ...doc }));
  const seen = new Set<string>();
  for (const doc of next) {
    const canonical =
      resolveLegalDocumentId(doc.id) ||
      normalizeLoginAgreementDocumentId(doc.id);
    if (!canonical) {
      continue;
    }
    seen.add(canonical);
    if (!doc.title.trim()) {
      const bundled = bundledLegalDocuments(locale).find((item) => item.id === canonical);
      if (bundled) {
        doc.title = bundled.title;
      }
    }
    if (!doc.content_md.trim()) {
      const bundled = bundledLegalDocuments(locale).find((item) => item.id === canonical);
      if (bundled) {
        doc.content_md = bundled.content_md;
      }
    }
  }
  for (const bundled of bundledLegalDocuments(locale)) {
    if (!seen.has(bundled.id)) {
      next.push({
        id: bundled.id,
        title: bundled.title,
        content_md: bundled.content_md,
      });
    }
  }
  return next;
}

function removeLoginAgreementDocument(index: number) {
  form.login_agreement_documents.splice(index, 1);
}

function normalizeLoginAgreementDocumentsForSave(): LoginAgreementDocument[] {
  return form.login_agreement_documents
    .map((doc, index) => ({
      id:
        normalizeLoginAgreementDocumentId(doc.id || doc.title) ||
        `doc-${index + 1}`,
      title: doc.title.trim(),
      content_md: doc.content_md.trim(),
    }))
    .filter((doc) => doc.title || doc.content_md);
}

function findDuplicateLoginAgreementDocumentId(
  documents: LoginAgreementDocument[],
): string | null {
  const seen = new Set<string>();
  for (const doc of documents) {
    if (seen.has(doc.id)) {
      return doc.id;
    }
    seen.add(doc.id);
  }
  return null;
}

function formatTablePageSizeOptions(options: number[]): string {
  return options.join(", ");
}

// ── codex_cli_only 黑/白名单结构化编辑（行 ↔ JSON）──
interface CodexClientRow {
  originator: string;
  uaContains: string; // 逗号分隔，序列化时拆成 ua_contains 数组
  skipEngineFingerprint?: boolean; // 仅白名单：命中即跳过引擎指纹门
}
const codexBlacklistRows = ref<CodexClientRow[]>([]);
const codexWhitelistRows = ref<CodexClientRow[]>([]);
const codexFingerprintRows = ref<FingerprintSignalRow[]>([]);
const codexFingerprintNoRequired = computed(
  () => !codexFingerprintRows.value.some((r) => r.required),
);
function addCodexFingerprintRow(): void {
  codexFingerprintRows.value.push({ type: "header_exact", match: "", required: false });
}
function removeCodexFingerprintRow(i: number): void {
  codexFingerprintRows.value.splice(i, 1);
}

function parseCodexEntriesToRows(raw: string): CodexClientRow[] {
  if (!raw || !raw.trim()) return [];
  try {
    const arr = JSON.parse(raw);
    if (!Array.isArray(arr)) return [];
    return arr.map((e) => ({
      originator: typeof e?.originator === "string" ? e.originator : "",
      uaContains: Array.isArray(e?.ua_contains)
        ? e.ua_contains
            .filter((x: unknown) => typeof x === "string")
            .join(", ")
        : "",
      skipEngineFingerprint: e?.skip_engine_fingerprint === true,
    }));
  } catch {
    return [];
  }
}

function serializeCodexRowsToJSON(rows: CodexClientRow[]): string {
  const entries = rows
    .map((r) => {
      const entry: {
        originator: string;
        ua_contains: string[];
        skip_engine_fingerprint?: boolean;
      } = {
        originator: r.originator.trim(),
        ua_contains: r.uaContains
          .split(",")
          .map((s) => s.trim())
          .filter((s) => s.length > 0),
      };
      if (r.skipEngineFingerprint) entry.skip_engine_fingerprint = true;
      return entry;
    })
    .filter((e) => e.originator !== "" || e.ua_contains.length > 0);
  return entries.length > 0 ? JSON.stringify(entries) : "";
}

function addCodexBlacklistRow(): void {
  codexBlacklistRows.value.push({ originator: "", uaContains: "" });
}
function removeCodexBlacklistRow(i: number): void {
  codexBlacklistRows.value.splice(i, 1);
}
function addCodexWhitelistRow(): void {
  codexWhitelistRows.value.push({
    originator: "",
    uaContains: "",
    skipEngineFingerprint: false,
  });
}
function removeCodexWhitelistRow(i: number): void {
  codexWhitelistRows.value.splice(i, 1);
}

const codexSyncedVersionLabel = computed(() => {
  const synced = form.openai_codex_client_version_synced?.trim();
  if (!synced) return "";
  return t("admin.settings.gatewayForwarding.openaiCodexVersionSyncedValue", {
    version: synced,
  });
});

async function loadSettings() {
  loading.value = true;
  loadFailed.value = false;
  try {
    const settings = await adminAPI.settings.getSettings();
    loadedSettingsSnapshot.value = JSON.parse(JSON.stringify(settings)) as Record<string, unknown>;
    settings.payment_load_balance_strategy =
      settings.payment_load_balance_strategy || "round-robin";
    // Only assign non-null values from backend (null means unconfigured, keep defaults)
    for (const [key, value] of Object.entries(settings)) {
      if (value !== null && value !== undefined) {
        (form as Record<string, unknown>)[key] = value;
      }
    }
    form.custom_endpoints = (form.custom_endpoints || []).map((ep) => ({
      name: ep.name || "",
      endpoint: ep.endpoint || "",
      description: ep.description || "",
      min_recharge:
        typeof ep.min_recharge === "number" ? ep.min_recharge : 1000,
    }));
    syncCaptchaProviderSelection();
    if (!form.claude_oauth_system_prompt_blocks?.trim()) {
      form.claude_oauth_system_prompt_blocks =
        defaultClaudeOAuthSystemPromptBlocks;
    }
    claudeOAuthSystemPromptBlocks.value = parseClaudeOAuthSystemPromptBlocks(
      form.claude_oauth_system_prompt_blocks,
      form.claude_oauth_system_prompt,
    );
    syncClaudeOAuthSystemPromptBlocksFormField();
    codexBlacklistRows.value = parseCodexEntriesToRows(
      form.codex_cli_only_blacklist,
    );
    codexWhitelistRows.value = parseCodexEntriesToRows(
      form.codex_cli_only_whitelist,
    );
    codexFingerprintRows.value = form.codex_cli_only_engine_fingerprint_signals
      ? parseFingerprintSignalsToRows(form.codex_cli_only_engine_fingerprint_signals)
      : defaultFingerprintSignalRows();
    form.login_agreement_mode =
      settings.login_agreement_mode === "checkbox" ? "checkbox" : "modal";
    form.channel_monitor_mode =
      settings.channel_monitor_mode === "v2" ? "v2" : "v1";
    form.channel_monitor_hide_throughput = Boolean(
      settings.channel_monitor_hide_throughput
    );
    form.channel_monitor_show_quota = Boolean(
      settings.channel_monitor_show_quota
    );
    form.channel_monitor_hide_user_ranking = Boolean(
      settings.channel_monitor_hide_user_ranking
    );
    form.login_agreement_updated_at =
      settings.login_agreement_updated_at || "2026-03-31";
    form.login_agreement_documents = ensureBundledLoginAgreementDocuments(
      Array.isArray(settings.login_agreement_documents) &&
        settings.login_agreement_documents.length > 0
        ? settings.login_agreement_documents.map((doc) => ({
            id: doc.id || "",
            title: doc.title || "",
            content_md: doc.content_md || "",
          }))
        : defaultLoginAgreementDocuments(),
    );
    Object.assign(authSourceDefaults, buildAuthSourceDefaultsState(settings));
    form.default_platform_quotas = normalizePlatformQuotasMap(settings.default_platform_quotas);
    form.account_scheduling_thresholds = normalizeAccountSchedulingThresholdsMap(
      settings.account_scheduling_thresholds,
    );
    form.backend_mode_enabled = settings.backend_mode_enabled;
    form.default_subscriptions = normalizeDefaultSubscriptionSettings(
      settings.default_subscriptions,
    );
    registrationEmailSuffixWhitelistTags.value =
      normalizeRegistrationEmailSuffixDomains(
        settings.registration_email_suffix_whitelist,
      );
    form.forwarded_client_ip_headers = normalizeForwardedClientIpHeaders(
      settings.forwarded_client_ip_headers,
    );
    forwardedClientIpHeaderDraft.value = "";
    tablePageSizeOptionsInput.value = formatTablePageSizeOptions(
      Array.isArray(settings.table_page_size_options)
        ? settings.table_page_size_options
        : [10, 20, 50, 100],
    );
    registrationEmailSuffixWhitelistDraft.value = "";
    form.smtp_password = "";
    smtpPasswordManuallyEdited.value = false;
    form.turnstile_secret_key = "";
    form.tencent_captcha_app_secret_key = "";
    form.tencent_captcha_cloud_secret_id = "";
    form.tencent_captcha_cloud_secret_key = "";
    form.aliyun_captcha_access_key_secret = "";
    form.linuxdo_connect_client_secret = "";
    form.dingtalk_connect_client_secret = "";
    form.github_oauth_client_secret = "";
    form.google_oauth_client_secret = "";
    form.wechat_connect_app_secret = "";
    form.wechat_connect_open_app_secret = "";
    form.wechat_connect_mp_app_secret = "";
    form.wechat_connect_mobile_app_secret = "";
    const wechatCapabilities = resolveWeChatConnectModeCapabilities(
      settings.wechat_connect_open_enabled,
      settings.wechat_connect_mp_enabled,
      settings.wechat_connect_mobile_enabled,
      settings.wechat_connect_mode,
    );
    form.wechat_connect_open_enabled = wechatCapabilities.openEnabled;
    form.wechat_connect_mp_enabled = wechatCapabilities.mpEnabled;
    form.wechat_connect_mobile_enabled = wechatCapabilities.mobileEnabled;
    form.wechat_connect_mode = deriveWeChatConnectStoredMode(
      wechatCapabilities.openEnabled,
      wechatCapabilities.mpEnabled,
      wechatCapabilities.mobileEnabled,
      settings.wechat_connect_mode,
    );
    const legacyWeChatAppID = String(settings.wechat_connect_app_id || "").trim();
    const legacyWeChatSecretConfigured = Boolean(
      settings.wechat_connect_app_secret_configured,
    );
    if (!form.wechat_connect_open_app_id && wechatCapabilities.openEnabled) {
      form.wechat_connect_open_app_id = legacyWeChatAppID;
    }
    if (!form.wechat_connect_mp_app_id && wechatCapabilities.mpEnabled) {
      form.wechat_connect_mp_app_id = legacyWeChatAppID;
    }
    if (!form.wechat_connect_mobile_app_id && wechatCapabilities.mobileEnabled) {
      form.wechat_connect_mobile_app_id = legacyWeChatAppID;
    }
    if (
      !form.wechat_connect_open_app_secret_configured &&
      wechatCapabilities.openEnabled
    ) {
      form.wechat_connect_open_app_secret_configured =
        legacyWeChatSecretConfigured;
    }
    if (
      !form.wechat_connect_mp_app_secret_configured &&
      wechatCapabilities.mpEnabled
    ) {
      form.wechat_connect_mp_app_secret_configured = legacyWeChatSecretConfigured;
    }
    if (
      !form.wechat_connect_mobile_app_secret_configured &&
      wechatCapabilities.mobileEnabled
    ) {
      form.wechat_connect_mobile_app_secret_configured =
        legacyWeChatSecretConfigured;
    }
    form.wechat_connect_scopes = defaultWeChatConnectScopesForMode(
      form.wechat_connect_mode,
    );
    form.oidc_connect_client_secret = "";

    // Load OpenAI fast/flex policy rules from bulk settings.
    // 仅当 payload 真的包含该字段时填充并标记为已加载；否则保持表单空值，
    // 让 saveSettings 在未加载时跳过该字段，防止覆盖后端默认规则。
    if (
      settings.openai_fast_policy_settings &&
      Array.isArray(settings.openai_fast_policy_settings.rules)
    ) {
      openaiFastPolicyForm.rules =
        settings.openai_fast_policy_settings.rules.map((rule) => ({
          ...rule,
          user_ids: rule.user_ids ? [...rule.user_ids] : [],
          model_whitelist: rule.model_whitelist
            ? [...rule.model_whitelist]
            : [],
        }));
      openaiFastPolicyLoaded.value = true;
    }

    // Load web search emulation config separately
    await loadWebSearchConfig();
    // Build the incremental baseline from the same normalisation the save path uses;
    // otherwise a save without edits would still look dirty (e.g. login documents).
    form.login_agreement_documents = normalizeLoginAgreementDocumentsForSave();
    loadedSettingsPayload.value = JSON.parse(JSON.stringify(buildSettingsPayload())) as Record<string, unknown>;
  } catch (error: unknown) {
    loadFailed.value = true;
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.failedToLoad")),
    );
  } finally {
    loading.value = false;
  }
}

async function loadSubscriptionGroups() {
  try {
    const groups = await adminAPI.groups.getAll();
    subscriptionGroups.value = groups.filter(
      (group) =>
        group.subscription_type === "subscription" && group.status === "active",
    );
  } catch (_error: unknown) {
    subscriptionGroups.value = [];
  }
}

function findNextAvailableSubscriptionGroup(
  existingGroupIDs: number[],
): AdminGroup | undefined {
  const existing = new Set(existingGroupIDs);
  return subscriptionGroups.value.find((group) => !existing.has(group.id));
}

function addDefaultSubscription() {
  if (subscriptionGroups.value.length === 0) return;
  const candidate = findNextAvailableSubscriptionGroup(
    form.default_subscriptions.map((item) => item.group_id),
  );
  if (!candidate) return;
  form.default_subscriptions.push({
    group_id: candidate.id,
    validity_days: 30,
  });
}

function removeDefaultSubscription(index: number) {
  form.default_subscriptions.splice(index, 1);
}

function addAuthSourceDefaultSubscription(source: AuthSourceType) {
  if (subscriptionGroups.value.length === 0) return;
  const candidate = findNextAvailableSubscriptionGroup(
    authSourceDefaults[source].subscriptions.map((item) => item.group_id),
  );
  if (!candidate) return;
  authSourceDefaults[source].subscriptions.push({
    group_id: candidate.id,
    validity_days: 30,
  });
}

function removeAuthSourceDefaultSubscription(
  source: AuthSourceType,
  index: number,
) {
  authSourceDefaults[source].subscriptions.splice(index, 1);
}

function findDuplicateDefaultSubscription(
  subscriptions: DefaultSubscriptionSetting[],
): DefaultSubscriptionSetting | undefined {
  const seenGroupIDs = new Set<number>();

  return subscriptions.find((item) => {
    if (seenGroupIDs.has(item.group_id)) {
      return true;
    }
    seenGroupIDs.add(item.group_id);
    return false;
  });
}

function buildSettingsPayload(): UpdateSettingsRequest {
  const normalizedDefaultSubscriptions = normalizeDefaultSubscriptionSettings(form.default_subscriptions);
  const wechatStoredMode = deriveWeChatConnectStoredMode(form.wechat_connect_open_enabled, form.wechat_connect_mp_enabled, form.wechat_connect_mobile_enabled, form.wechat_connect_mode);
  const claudeOAuthSystemPromptBlocksJSON = serializeClaudeOAuthSystemPromptBlocksToJSON(claudeOAuthSystemPromptBlocks.value);
    const payload: UpdateSettingsRequest = {
      registration_enabled: form.registration_enabled,
      email_verify_enabled: form.email_verify_enabled,
      registration_email_suffix_whitelist:
        registrationEmailSuffixWhitelistTags.value.map((suffix) =>
          suffix.startsWith("*.") ? suffix : `@${suffix}`,
        ),
      registration_email_domain_quota_enabled:
        form.registration_email_domain_quota_enabled,
      registration_email_alias_block_enabled: form.registration_email_alias_block_enabled,
      promo_code_enabled: form.promo_code_enabled,
      invitation_code_enabled: form.invitation_code_enabled,
      password_reset_enabled: form.password_reset_enabled,
      totp_enabled: form.totp_enabled,
      passkey_enabled: form.passkey_enabled,
      session_binding_enabled: form.session_binding_enabled,
      step_up_enabled: form.step_up_enabled,
      // 清空数字框时 v-model.number 会得到空串，后端 int 字段解析空串会 400 拒绝整次保存；
      // 空/非法值回退默认 180（与后端 parseAuditLogRetentionDays("") 语义一致，0 仍表示永久保留）。
      audit_log_retention_days: Number.isFinite(form.audit_log_retention_days)
        ? form.audit_log_retention_days
        : 180,
      login_agreement_enabled: form.login_agreement_enabled,
      login_agreement_mode: form.login_agreement_mode,
      login_agreement_updated_at: form.login_agreement_updated_at,
      login_agreement_documents: form.login_agreement_documents,
      default_balance: form.default_balance,
      affiliate_rebate_rate: Math.min(
        100,
        Math.max(0, Number(form.affiliate_rebate_rate) || 0),
      ),
      affiliate_rebate_freeze_hours: Math.max(0, Math.min(720, Number(form.affiliate_rebate_freeze_hours) || 0)),
      affiliate_rebate_duration_days: Math.max(0, Math.min(3650, Math.floor(Number(form.affiliate_rebate_duration_days) || 0))),
      affiliate_rebate_per_invitee_cap: Math.max(0, Number(form.affiliate_rebate_per_invitee_cap) || 0),
      affiliate_admin_recharge_enabled: form.affiliate_admin_recharge_enabled,
      default_concurrency: form.default_concurrency,
      default_subscriptions: normalizedDefaultSubscriptions,
      force_email_on_third_party_signup: form.force_email_on_third_party_signup,
      default_user_rpm_limit: form.default_user_rpm_limit,
      site_name: form.site_name,
      site_logo: form.site_logo,
      site_subtitle: form.site_subtitle,
      api_base_url: form.api_base_url,
      contact_info: form.contact_info,
      doc_url: form.doc_url,
      home_content: form.home_content,
      compact_home_enabled: form.compact_home_enabled,
      backend_mode_enabled: form.backend_mode_enabled,
      hide_ccs_import_button: form.hide_ccs_import_button,
      table_default_page_size: form.table_default_page_size,
      table_page_size_options: form.table_page_size_options,
      custom_menu_items: form.custom_menu_items,
      custom_endpoints: form.custom_endpoints.map((ep) => ({
        name: ep.name,
        endpoint: ep.endpoint,
        description: ep.description,
        min_recharge:
          typeof ep.min_recharge === "number" &&
          Number.isFinite(ep.min_recharge)
            ? ep.min_recharge
            : 1000,
      })),
      ticket_admin_email: form.ticket_admin_email,
      frontend_url: form.frontend_url,
      smtp_host: form.smtp_host,
      smtp_port: form.smtp_port,
      smtp_username: form.smtp_username,
      smtp_password: form.smtp_password || undefined,
      smtp_from_email: form.smtp_from_email,
      smtp_from_name: form.smtp_from_name,
      smtp_use_tls: form.smtp_use_tls,
      turnstile_enabled: form.turnstile_enabled,
      turnstile_site_key: form.turnstile_site_key,
      turnstile_secret_key: form.turnstile_secret_key || undefined,
      tencent_captcha_enabled: form.tencent_captcha_enabled,
      tencent_captcha_app_id: form.tencent_captcha_app_id,
      tencent_captcha_app_secret_key:
        form.tencent_captcha_app_secret_key || undefined,
      tencent_captcha_cloud_secret_id:
        form.tencent_captcha_cloud_secret_id || undefined,
      tencent_captcha_cloud_secret_key:
        form.tencent_captcha_cloud_secret_key || undefined,
      tencent_captcha_region: form.tencent_captcha_region,
      aliyun_captcha_enabled: form.aliyun_captcha_enabled,
      aliyun_captcha_access_key_id: form.aliyun_captcha_access_key_id,
      aliyun_captcha_access_key_secret:
        form.aliyun_captcha_access_key_secret || undefined,
      aliyun_captcha_scene_id: form.aliyun_captcha_scene_id,
      aliyun_captcha_prefix: form.aliyun_captcha_prefix,
      aliyun_captcha_region: form.aliyun_captcha_region,
      api_key_acl_trust_forwarded_ip: form.api_key_acl_trust_forwarded_ip,
      forwarded_client_ip_headers: form.forwarded_client_ip_headers,
      linuxdo_connect_enabled: form.linuxdo_connect_enabled,
      linuxdo_connect_client_id: form.linuxdo_connect_client_id,
      linuxdo_connect_client_secret:
        form.linuxdo_connect_client_secret || undefined,
      linuxdo_connect_redirect_url: form.linuxdo_connect_redirect_url,
      dingtalk_connect_enabled: form.dingtalk_connect_enabled,
      dingtalk_connect_client_id: form.dingtalk_connect_client_id,
      dingtalk_connect_client_secret:
        form.dingtalk_connect_client_secret || undefined,
      dingtalk_connect_redirect_url: form.dingtalk_connect_redirect_url,
      dingtalk_connect_corp_restriction_policy:
        form.dingtalk_connect_corp_restriction_policy,
      dingtalk_connect_internal_corp_id: form.dingtalk_connect_internal_corp_id,
      dingtalk_connect_bypass_registration: form.dingtalk_connect_bypass_registration,
      dingtalk_connect_sync_corp_email: form.dingtalk_connect_sync_corp_email,
      dingtalk_connect_sync_display_name: form.dingtalk_connect_sync_display_name,
      dingtalk_connect_sync_dept: form.dingtalk_connect_sync_dept,
      dingtalk_connect_sync_corp_email_attr_key: form.dingtalk_connect_sync_corp_email_attr_key,
      dingtalk_connect_sync_display_name_attr_key: form.dingtalk_connect_sync_display_name_attr_key,
      dingtalk_connect_sync_dept_attr_key: form.dingtalk_connect_sync_dept_attr_key,
      dingtalk_connect_sync_corp_email_attr_name: form.dingtalk_connect_sync_corp_email_attr_name,
      dingtalk_connect_sync_display_name_attr_name: form.dingtalk_connect_sync_display_name_attr_name,
      dingtalk_connect_sync_dept_attr_name: form.dingtalk_connect_sync_dept_attr_name,
      wechat_connect_enabled: form.wechat_connect_enabled,
      wechat_connect_app_id:
        form.wechat_connect_open_app_id ||
        form.wechat_connect_mp_app_id ||
        form.wechat_connect_mobile_app_id ||
        form.wechat_connect_app_id,
      wechat_connect_app_secret: form.wechat_connect_app_secret || undefined,
      wechat_connect_open_app_id: form.wechat_connect_open_app_id,
      wechat_connect_open_app_secret:
        form.wechat_connect_open_app_secret || undefined,
      wechat_connect_mp_app_id: form.wechat_connect_mp_app_id,
      wechat_connect_mp_app_secret:
        form.wechat_connect_mp_app_secret || undefined,
      wechat_connect_mobile_app_id: form.wechat_connect_mobile_app_id,
      wechat_connect_mobile_app_secret:
        form.wechat_connect_mobile_app_secret || undefined,
      wechat_connect_open_enabled: form.wechat_connect_open_enabled,
      wechat_connect_mp_enabled: form.wechat_connect_mp_enabled,
      wechat_connect_mobile_enabled: form.wechat_connect_mobile_enabled,
      wechat_connect_mode: wechatStoredMode,
      wechat_connect_scopes:
        defaultWeChatConnectScopesForMode(wechatStoredMode),
      wechat_connect_redirect_url: form.wechat_connect_redirect_url,
      wechat_connect_frontend_redirect_url:
        form.wechat_connect_frontend_redirect_url,
      oidc_connect_enabled: form.oidc_connect_enabled,
      oidc_connect_provider_name: form.oidc_connect_provider_name,
      oidc_connect_client_id: form.oidc_connect_client_id,
      oidc_connect_client_secret: form.oidc_connect_client_secret || undefined,
      oidc_connect_issuer_url: form.oidc_connect_issuer_url,
      oidc_connect_discovery_url: form.oidc_connect_discovery_url,
      oidc_connect_authorize_url: form.oidc_connect_authorize_url,
      oidc_connect_token_url: form.oidc_connect_token_url,
      oidc_connect_userinfo_url: form.oidc_connect_userinfo_url,
      oidc_connect_jwks_url: form.oidc_connect_jwks_url,
      oidc_connect_scopes: form.oidc_connect_scopes,
      oidc_connect_redirect_url: form.oidc_connect_redirect_url,
      oidc_connect_frontend_redirect_url:
        form.oidc_connect_frontend_redirect_url,
      oidc_connect_token_auth_method: form.oidc_connect_token_auth_method,
      oidc_connect_use_pkce: form.oidc_connect_use_pkce,
      oidc_connect_validate_id_token: form.oidc_connect_validate_id_token,
      oidc_connect_allowed_signing_algs: form.oidc_connect_allowed_signing_algs,
      oidc_connect_clock_skew_seconds: form.oidc_connect_clock_skew_seconds,
      oidc_connect_require_email_verified:
        form.oidc_connect_require_email_verified,
      oidc_connect_userinfo_email_path: form.oidc_connect_userinfo_email_path,
      oidc_connect_userinfo_id_path: form.oidc_connect_userinfo_id_path,
      oidc_connect_userinfo_username_path:
        form.oidc_connect_userinfo_username_path,
      github_oauth_enabled: form.github_oauth_enabled,
      github_oauth_client_id: form.github_oauth_client_id,
      github_oauth_client_secret:
        form.github_oauth_client_secret || undefined,
      github_oauth_redirect_url: form.github_oauth_redirect_url,
      github_oauth_frontend_redirect_url:
        form.github_oauth_frontend_redirect_url,
      google_oauth_enabled: form.google_oauth_enabled,
      google_oauth_client_id: form.google_oauth_client_id,
      google_oauth_client_secret:
        form.google_oauth_client_secret || undefined,
      google_oauth_redirect_url: form.google_oauth_redirect_url,
      google_oauth_frontend_redirect_url:
        form.google_oauth_frontend_redirect_url,
      enable_model_fallback: form.enable_model_fallback,
      fallback_model_anthropic: form.fallback_model_anthropic,
      fallback_model_openai: form.fallback_model_openai,
      fallback_model_gemini: form.fallback_model_gemini,
      fallback_model_antigravity: form.fallback_model_antigravity,
      grok_default_text_model:
        form.grok_default_text_model.trim() || "grok-4.5",
      grok_cross_client_model_map_enabled:
        form.grok_cross_client_model_map_enabled,
      grok_default_base_url_mode: form.grok_default_base_url_mode,
      enable_identity_patch: form.enable_identity_patch,
      identity_patch_prompt: form.identity_patch_prompt,
      min_claude_code_version: form.min_claude_code_version,
      max_claude_code_version: form.max_claude_code_version,
      allow_ungrouped_key_scheduling: form.allow_ungrouped_key_scheduling,
      openai_ttft_mode:
        form.openai_ttft_mode === "visible" ? "visible" : "semantic",
      enable_fingerprint_unification: form.enable_fingerprint_unification,
      enable_metadata_passthrough: form.enable_metadata_passthrough,
      enable_cch_signing: form.enable_cch_signing,
      enable_claude_oauth_system_prompt_injection:
        form.enable_claude_oauth_system_prompt_injection,
      claude_oauth_system_prompt: form.claude_oauth_system_prompt?.trim()
        ? form.claude_oauth_system_prompt
        : "",
      claude_oauth_system_prompt_blocks: claudeOAuthSystemPromptBlocksJSON,
      enable_anthropic_cache_ttl_1h_injection:
        form.enable_anthropic_cache_ttl_1h_injection,
      billing_balance_preauthorization_enabled:
        form.billing_balance_preauthorization_enabled,
      billing_inflight_reservation_enabled:
        form.billing_inflight_reservation_enabled,
      rewrite_message_cache_control: form.rewrite_message_cache_control,
      enable_client_dateline_normalization:
        form.enable_client_dateline_normalization,
      antigravity_user_agent_version:
        form.antigravity_user_agent_version?.trim() || "",
      openai_codex_user_agent:
        form.openai_codex_user_agent?.trim() || "",
      openai_codex_client_version:
        form.openai_codex_client_version?.trim() || "",
      openai_codex_version_auto_sync_enabled:
        form.openai_codex_version_auto_sync_enabled,
      openai_codex_ticket_enabled: form.openai_codex_ticket_enabled,
      openai_codex_ticket_harvest_proxy_url:
        form.openai_codex_ticket_harvest_proxy_url?.trim() || "",
      min_codex_version: form.min_codex_version?.trim() || "",
      max_codex_version: form.max_codex_version?.trim() || "",
      codex_cli_only_allow_app_server_clients:
        form.codex_cli_only_allow_app_server_clients,
      codex_cli_only_engine_fingerprint_signals: serializeFingerprintRowsToJSON(
        codexFingerprintRows.value,
      ),
      codex_cli_only_blacklist: serializeCodexRowsToJSON(
        codexBlacklistRows.value,
      ),
      codex_cli_only_whitelist: serializeCodexRowsToJSON(
        codexWhitelistRows.value,
      ),
      // Payment configuration
      payment_enabled: form.payment_enabled,
      payment_recharge_center_enabled: form.payment_recharge_center_enabled,
      risk_control_enabled: form.risk_control_enabled,
      cyber_session_block_enabled: form.cyber_session_block_enabled,
      cyber_session_block_ttl_seconds:
        Number(form.cyber_session_block_ttl_seconds) || 3600,
      payment_min_amount: Number(form.payment_min_amount) || 0,
      payment_max_amount: Number(form.payment_max_amount) || 0,
      payment_daily_limit: Number(form.payment_daily_limit) || 0,
      payment_max_pending_orders: Number(form.payment_max_pending_orders) || 0,
      payment_order_timeout_minutes:
        Number(form.payment_order_timeout_minutes) || 0,
      payment_balance_disabled: form.payment_balance_disabled,
      payment_balance_recharge_multiplier:
        Number(form.payment_balance_recharge_multiplier) || 1,
      payment_subscription_usd_to_cny_rate:
        Number(form.payment_subscription_usd_to_cny_rate) || 0,
      payment_recharge_fee_rate: Number(form.payment_recharge_fee_rate) || 0,
      payment_enabled_types: form.payment_enabled_types,
      payment_load_balance_strategy: form.payment_load_balance_strategy,
      payment_product_name_prefix: form.payment_product_name_prefix,
      payment_product_name_suffix: form.payment_product_name_suffix,
      payment_help_image_url: form.payment_help_image_url,
      payment_help_text: form.payment_help_text,
      payment_cancel_rate_limit_enabled: form.payment_cancel_rate_limit_enabled,
      payment_cancel_rate_limit_max:
        Number(form.payment_cancel_rate_limit_max) || 10,
      payment_cancel_rate_limit_window:
        Number(form.payment_cancel_rate_limit_window) || 1,
      payment_cancel_rate_limit_unit: form.payment_cancel_rate_limit_unit,
      payment_cancel_rate_limit_window_mode:
        form.payment_cancel_rate_limit_window_mode,
      payment_alipay_force_qrcode: form.payment_alipay_force_qrcode,
      payment_alipay_mobile_precreate_deep_link:
        form.payment_alipay_mobile_precreate_deep_link,
      openai_low_upstream_rate_priority_enabled:
        form.openai_low_upstream_rate_priority_enabled,
      openai_oauth_scheduling_rate_multiplier:
        form.openai_oauth_scheduling_rate_multiplier,
      codex_quota_overdraft_enabled: form.codex_quota_overdraft_enabled,
      // Keep the subordinate preference when the master switch is temporarily
      // off; runtime still requires master=true before any injection occurs.
      codex_quota_overdraft_business_injection_enabled:
        form.codex_quota_overdraft_business_injection_enabled,
      openai_advanced_scheduler_enabled: form.openai_advanced_scheduler_enabled,
      openai_advanced_scheduler_sticky_weighted_enabled:
        form.openai_advanced_scheduler_sticky_weighted_enabled,
      openai_advanced_scheduler_subscription_priority_enabled:
        form.openai_advanced_scheduler_subscription_priority_enabled,
      openai_advanced_scheduler_lb_top_k:
        form.openai_advanced_scheduler_lb_top_k.trim(),
      openai_advanced_scheduler_weight_priority:
        form.openai_advanced_scheduler_weight_priority.trim(),
      openai_advanced_scheduler_weight_load:
        form.openai_advanced_scheduler_weight_load.trim(),
      openai_advanced_scheduler_weight_queue:
        form.openai_advanced_scheduler_weight_queue.trim(),
      openai_advanced_scheduler_weight_error_rate:
        form.openai_advanced_scheduler_weight_error_rate.trim(),
      openai_advanced_scheduler_weight_ttft:
        form.openai_advanced_scheduler_weight_ttft.trim(),
      openai_advanced_scheduler_weight_reset:
        form.openai_advanced_scheduler_weight_reset.trim(),
      openai_advanced_scheduler_weight_quota_headroom:
        form.openai_advanced_scheduler_weight_quota_headroom.trim(),
      openai_advanced_scheduler_weight_upstream_cost:
        form.openai_advanced_scheduler_weight_upstream_cost.trim(),
      openai_advanced_scheduler_weight_previous_response:
        form.openai_advanced_scheduler_weight_previous_response.trim(),
      openai_advanced_scheduler_weight_session_sticky:
        form.openai_advanced_scheduler_weight_session_sticky.trim(),
      // 余额、订阅到期与账号限额通知
      balance_low_notify_enabled: form.balance_low_notify_enabled,
      balance_low_notify_threshold:
        Number(form.balance_low_notify_threshold) || 0,
      balance_low_notify_recharge_url: (form.balance_low_notify_recharge_url =
        form.balance_low_notify_recharge_url || currentOrigin),
      subscription_expiry_notify_enabled:
        form.subscription_expiry_notify_enabled,
      account_quota_notify_enabled: form.account_quota_notify_enabled,
      account_quota_notify_emails: (
        form.account_quota_notify_emails || []
      ).filter((e) => e.email.trim() !== ""),
      // Channel Monitor feature switch
      channel_monitor_enabled: form.channel_monitor_enabled,
      channel_monitor_mode: form.channel_monitor_mode === 'v1' ? 'v1' : 'v2',
      channel_monitor_default_interval_seconds:
        Number(form.channel_monitor_default_interval_seconds) || 60,
      channel_monitor_hide_throughput: Boolean(form.channel_monitor_hide_throughput),
      channel_monitor_show_quota: Boolean(form.channel_monitor_show_quota),
      channel_monitor_hide_user_ranking: Boolean(form.channel_monitor_hide_user_ranking),
      // Available Channels feature switch
      available_channels_enabled: form.available_channels_enabled,
      // Model Plaza feature switches + description
      model_plaza_enabled: form.model_plaza_enabled,
      model_plaza_require_auth: form.model_plaza_require_auth,
      model_plaza_description: form.model_plaza_description,
      plugin_management_enabled: form.plugin_management_enabled,
      // Affiliate (邀请返利) feature switch
      affiliate_enabled: form.affiliate_enabled,
      allow_user_view_error_requests: form.allow_user_view_error_requests,
    };

    // 仅当 openai_fast_policy_settings 已成功从后端加载时才回写，
    // 否则省略整个字段，让后端保留既有规则（含默认值）。
    if (openaiFastPolicyLoaded.value) {
      payload.openai_fast_policy_settings = {
        rules: openaiFastPolicyForm.rules.map((rule) => {
          const whitelist = (rule.model_whitelist || [])
            .map((p) => p.trim())
            .filter((p) => p !== "");
          const hasWhitelist = whitelist.length > 0;
          return {
            service_tier: rule.service_tier,
            action: rule.action,
            scope: rule.scope,
            user_ids:
              rule.user_ids && rule.user_ids.length > 0
                ? [...rule.user_ids]
                : undefined,
            error_message:
              rule.action === "block" ? rule.error_message : undefined,
            model_whitelist: hasWhitelist ? whitelist : undefined,
            fallback_action: hasWhitelist
              ? rule.fallback_action || "pass"
              : undefined,
            fallback_error_message:
              hasWhitelist && rule.fallback_action === "block"
                ? rule.fallback_error_message
                : undefined,
          };
        }),
      };
    }

    payload.default_platform_quotas = sanitizePlatformQuotasMap(form.default_platform_quotas);
    payload.account_scheduling_thresholds = sanitizeAccountSchedulingThresholdsMap(
      form.account_scheduling_thresholds,
    );
    appendAuthSourceDefaultsToUpdateRequest(payload, authSourceDefaults);

  return payload;
}

async function saveSettings() {
  if (saving.value || loading.value || loadFailed.value) return;
  saving.value = true;
  try {
    const normalizedTableDefaultPageSize = parseTableDefaultPageSizeInput(form.table_default_page_size);
    if (normalizedTableDefaultPageSize === null) {
      appStore.showError(
        t("admin.settings.site.tableDefaultPageSizeRangeError", {
          min: tablePageSizeMin,
          max: tablePageSizeMax,
        }),
      );
      return;
    }

    const normalizedTablePageSizeOptions = parseTablePageSizeOptionsInput(
      tablePageSizeOptionsInput.value,
    );
    if (!normalizedTablePageSizeOptions) {
      appStore.showError(
        t("admin.settings.site.tablePageSizeOptionsFormatError", {
          min: tablePageSizeMin,
          max: tablePageSizeMax,
        }),
      );
      return;
    }

    form.table_default_page_size = normalizedTableDefaultPageSize;
    form.table_page_size_options = normalizedTablePageSizeOptions;

    const normalizedLoginAgreementDocuments =
      normalizeLoginAgreementDocumentsForSave();
    if (form.login_agreement_enabled && normalizedLoginAgreementDocuments.length === 0) {
      appStore.showError(
        localText(
          "启用登录条款确认时，至少需要保留一份文档。",
          "At least one document is required when login agreement is enabled.",
        ),
      );
      return;
    }
    const emptyTitleDocument = normalizedLoginAgreementDocuments.find(
      (doc) => !doc.title,
    );
    if (emptyTitleDocument) {
      appStore.showError(
        localText(
          "登录条款文档名称不能为空。",
          "Login agreement document title cannot be empty.",
        ),
      );
      return;
    }
    const duplicateLoginAgreementDocumentId =
      findDuplicateLoginAgreementDocumentId(normalizedLoginAgreementDocuments);
    if (duplicateLoginAgreementDocumentId) {
      appStore.showError(
        localText(
          `登录条款文档路由不能重复：/legal/${duplicateLoginAgreementDocumentId}`,
          `Login agreement document routes cannot be duplicated: /legal/${duplicateLoginAgreementDocumentId}`,
        ),
      );
      return;
    }
    form.login_agreement_mode =
      form.login_agreement_mode === "checkbox" ? "checkbox" : "modal";
    form.login_agreement_documents = normalizedLoginAgreementDocuments;
    form.forwarded_client_ip_headers = normalizeForwardedClientIpHeaders(
      form.forwarded_client_ip_headers,
    );

    const normalizedDefaultSubscriptions = normalizeDefaultSubscriptionSettings(
      form.default_subscriptions,
    );
    const duplicateDefaultSubscription = findDuplicateDefaultSubscription(
      normalizedDefaultSubscriptions,
    );
    if (duplicateDefaultSubscription) {
      appStore.showError(
        t("admin.settings.defaults.defaultSubscriptionsDuplicate", {
          groupId: duplicateDefaultSubscription.group_id,
        }),
      );
      return;
    }

    for (const authSource of authSourceDefaultsMeta.value) {
      authSourceDefaults[authSource.source].subscriptions =
        normalizeDefaultSubscriptionSettings(
          authSourceDefaults[authSource.source].subscriptions,
        );
      const duplicate = findDuplicateDefaultSubscription(
        authSourceDefaults[authSource.source].subscriptions,
      );
      if (duplicate) {
        appStore.showError(
          `${authSource.title}: ${t(
            "admin.settings.defaults.defaultSubscriptionsDuplicate",
            {
              groupId: duplicate.group_id,
            },
          )}`,
        );
        return;
      }
    }

    if (form.wechat_connect_mp_enabled && form.wechat_connect_mobile_enabled) {
      appStore.showError(
        localText(
          "公众号和移动应用不能同时启用。",
          "Official Account and Mobile App cannot be enabled at the same time.",
        ),
      );
      return;
    }
    // Validate URL fields — novalidate disables browser-native checks, so we validate here
    const isValidHttpUrl = (url: string): boolean => {
      if (!url) return true;
      try {
        const u = new URL(url);
        return u.protocol === "http:" || u.protocol === "https:";
      } catch {
        return false;
      }
    };
    // Optional URL fields: auto-clear invalid values so they don't cause backend 400 errors
    if (!isValidHttpUrl(form.frontend_url)) form.frontend_url = "";
    if (!isValidHttpUrl(form.doc_url)) form.doc_url = "";
    syncWeChatConnectMode();
    const claudeOAuthSystemPromptBlocksJSON =
      serializeClaudeOAuthSystemPromptBlocksToJSON(
        claudeOAuthSystemPromptBlocks.value,
      );
    form.claude_oauth_system_prompt_blocks =
      claudeOAuthSystemPromptBlocksJSON;

    const payload = buildSettingsPayload();

    const changedPayload = Object.fromEntries(
      Object.entries(payload).filter(([key, value]) =>
        stableSettingsValue(value) !== stableSettingsValue(loadedSettingsPayload.value[key]),
      ),
    ) as UpdateSettingsRequest;
    const hasSettingsChanges = Object.keys(changedPayload).length > 0;
    // No-op: skip the PUT entirely and keep the previous baseline so the next
    // real edit still diffs against what the server last confirmed.
    const updated = hasSettingsChanges
      ? await settingsStepUp.run(() => adminAPI.settings.updateSettings(changedPayload))
      : (loadedSettingsSnapshot.value as unknown as SystemSettings);
    for (const [key, value] of Object.entries(updated)) {
      if (key === "openai_fast_policy_settings") continue;
      if (value !== null && value !== undefined) {
        (form as Record<string, unknown>)[key] = value;
      }
    }
    Object.assign(authSourceDefaults, buildAuthSourceDefaultsState(updated));
    form.default_platform_quotas = normalizePlatformQuotasMap(updated.default_platform_quotas);
    form.account_scheduling_thresholds = normalizeAccountSchedulingThresholdsMap(
      updated.account_scheduling_thresholds,
    );
    registrationEmailSuffixWhitelistTags.value =
      normalizeRegistrationEmailSuffixDomains(
        updated.registration_email_suffix_whitelist,
      );
    form.forwarded_client_ip_headers = normalizeForwardedClientIpHeaders(
      updated.forwarded_client_ip_headers,
    );
    forwardedClientIpHeaderDraft.value = "";
    tablePageSizeOptionsInput.value = formatTablePageSizeOptions(
      Array.isArray(updated.table_page_size_options)
        ? updated.table_page_size_options
        : [10, 20, 50, 100],
    );
    registrationEmailSuffixWhitelistDraft.value = "";
    form.smtp_password = "";
    smtpPasswordManuallyEdited.value = false;
    form.turnstile_secret_key = "";
    form.aliyun_captcha_access_key_secret = "";
    form.linuxdo_connect_client_secret = "";
    form.dingtalk_connect_client_secret = "";
    form.github_oauth_client_secret = "";
    form.google_oauth_client_secret = "";
    form.wechat_connect_app_secret = "";
    form.wechat_connect_open_app_secret = "";
    form.wechat_connect_mp_app_secret = "";
    form.wechat_connect_mobile_app_secret = "";
    const updatedWechatCapabilities = resolveWeChatConnectModeCapabilities(
      updated.wechat_connect_open_enabled,
      updated.wechat_connect_mp_enabled,
      updated.wechat_connect_mobile_enabled,
      updated.wechat_connect_mode,
    );
    form.wechat_connect_open_enabled = updatedWechatCapabilities.openEnabled;
    form.wechat_connect_mp_enabled = updatedWechatCapabilities.mpEnabled;
    form.wechat_connect_mobile_enabled =
      updatedWechatCapabilities.mobileEnabled;
    form.wechat_connect_mode = deriveWeChatConnectStoredMode(
      updatedWechatCapabilities.openEnabled,
      updatedWechatCapabilities.mpEnabled,
      updatedWechatCapabilities.mobileEnabled,
      updated.wechat_connect_mode,
    );
    form.wechat_connect_scopes = defaultWeChatConnectScopesForMode(
      form.wechat_connect_mode,
    );
    form.oidc_connect_client_secret = "";
    // Refresh OpenAI fast/flex policy from server response
    if (
      updated.openai_fast_policy_settings &&
      Array.isArray(updated.openai_fast_policy_settings.rules)
    ) {
      openaiFastPolicyForm.rules =
        updated.openai_fast_policy_settings.rules.map((rule) => ({
          ...rule,
          user_ids: rule.user_ids ? [...rule.user_ids] : [],
          model_whitelist: rule.model_whitelist
            ? [...rule.model_whitelist]
            : [],
        }));
      openaiFastPolicyLoaded.value = true;
    }
    // The baseline only moves once the response has been applied to the form, so a
    // failed save can be retried with the same diff instead of sending nothing.
    if (hasSettingsChanges) {
      loadedSettingsSnapshot.value = { ...loadedSettingsSnapshot.value, ...updated };
      loadedSettingsPayload.value = JSON.parse(JSON.stringify(buildSettingsPayload())) as Record<string, unknown>;
    }

    // Save web search emulation config separately (errors handled internally)
    const wsResult = await saveWebSearchConfig();
    if (hasSettingsChanges) {
      // Merge the returned values locally; avoid two extra full-page settings requests.
      // Only keys already cached as public are accepted, so no secret can leak in.
      const publicPatch: Record<string, unknown> = {};
      const cached = appStore.cachedPublicSettings;
      if (cached) {
        for (const key of Object.keys(cached)) {
          if (!Object.prototype.hasOwnProperty.call(updated, key)) continue;
          const value = (updated as unknown as Record<string, unknown>)[key];
          if (value !== undefined) publicPatch[key] = value;
        }
        if (Array.isArray(updated.custom_menu_items)) {
          publicPatch.custom_menu_items = updated.custom_menu_items.filter((item) => item.visibility === "user");
        }
      }
      appStore.patchPublicSettings(publicPatch);
      adminSettingsStore.patchFromSettings(updated);
    }
    if (hasSettingsChanges || wsResult === "saved") {
      appStore.showSuccess(t("admin.settings.settingsSaved"));
    }
  } catch (error: unknown) {
    // 用户取消 step-up 验证：静默返回，不弹错误
    if (isStepUpCancelled(error)) {
      return;
    }
    if (isStepUpBlocked(error)) {
      appStore.showError(
        stepUpBlockReason(error) === "STEP_UP_ADMIN_API_KEY_FORBIDDEN"
          ? t("stepUp.adminApiKeyForbidden")
          : t("stepUp.notEnabled"),
      );
      return;
    }
    // 开启 step-up 开关但本人未启用 2FA：给出可操作的专用提示
    if (
      (error as { reason?: string })?.reason === "STEP_UP_ENABLE_REQUIRES_TOTP"
    ) {
      appStore.showError(t("admin.settings.security.stepUpEnableRequiresTotp"));
      return;
    }
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.failedToSave")),
    );
  } finally {
    saving.value = false;
  }
}

async function testSmtpConnection() {
  testingSmtp.value = true;
  try {
    const smtpPasswordForTest = smtpPasswordManuallyEdited.value
      ? form.smtp_password
      : "";
    const result = await adminAPI.settings.testSmtpConnection({
      smtp_host: form.smtp_host,
      smtp_port: form.smtp_port,
      smtp_username: form.smtp_username,
      smtp_password: smtpPasswordForTest,
      smtp_use_tls: form.smtp_use_tls,
    });
    // API returns { message: "..." } on success, errors are thrown as exceptions
    appStore.showSuccess(
      result.message || t("admin.settings.smtpConnectionSuccess"),
    );
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.failedToTestSmtp")),
    );
  } finally {
    testingSmtp.value = false;
  }
}

async function sendTestEmail() {
  if (!testEmailAddress.value) {
    appStore.showError(t("admin.settings.testEmail.enterRecipientHint"));
    return;
  }

  sendingTestEmail.value = true;
  try {
    const smtpPasswordForSend = smtpPasswordManuallyEdited.value
      ? form.smtp_password
      : "";
    const result = await adminAPI.settings.sendTestEmail({
      email: testEmailAddress.value,
      smtp_host: form.smtp_host,
      smtp_port: form.smtp_port,
      smtp_username: form.smtp_username,
      smtp_password: smtpPasswordForSend,
      smtp_from_email: form.smtp_from_email,
      smtp_from_name: form.smtp_from_name,
      smtp_use_tls: form.smtp_use_tls,
    });
    // API returns { message: "..." } on success, errors are thrown as exceptions
    appStore.showSuccess(result.message || t("admin.settings.testEmailSent"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.failedToSendTestEmail")),
    );
  } finally {
    sendingTestEmail.value = false;
  }
}

// Admin API Key 方法
async function loadAdminApiKey() {
  adminApiKeyLoading.value = true;
  try {
    const status = await adminAPI.settings.getAdminApiKey();
    adminApiKeyExists.value = status.exists;
    adminApiKeyMasked.value = status.masked_key;
  } catch (_error: unknown) {
    // Silent fail - admin API key status is non-critical
  } finally {
    adminApiKeyLoading.value = false;
  }
}

async function createAdminApiKey() {
  adminApiKeyOperating.value = true;
  try {
    const result = await adminAPI.settings.regenerateAdminApiKey();
    newAdminApiKey.value = result.key;
    adminApiKeyExists.value = true;
    adminApiKeyMasked.value =
      result.key.substring(0, 10) + "..." + result.key.slice(-4);
    appStore.showSuccess(t("admin.settings.adminApiKey.keyGenerated"));
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t("common.error")));
  } finally {
    adminApiKeyOperating.value = false;
  }
}

async function regenerateAdminApiKey() {
  if (!confirm(t("admin.settings.adminApiKey.regenerateConfirm"))) return;
  await createAdminApiKey();
}

async function deleteAdminApiKey() {
  if (!confirm(t("admin.settings.adminApiKey.deleteConfirm"))) return;
  adminApiKeyOperating.value = true;
  try {
    await adminAPI.settings.deleteAdminApiKey();
    adminApiKeyExists.value = false;
    adminApiKeyMasked.value = "";
    newAdminApiKey.value = "";
    appStore.showSuccess(t("admin.settings.adminApiKey.keyDeleted"));
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t("common.error")));
  } finally {
    adminApiKeyOperating.value = false;
  }
}

function copyNewKey() {
  navigator.clipboard
    .writeText(newAdminApiKey.value)
    .then(() => {
      appStore.showSuccess(t("admin.settings.adminApiKey.keyCopied"));
    })
    .catch(() => {
      appStore.showError(t("common.copyFailed"));
    });
}

async function loadUpstreamBillingProbeSettings() {
  upstreamBillingProbeLoading.value = true;
  try {
    Object.assign(
      upstreamBillingProbeForm,
      await adminAPI.accounts.getUpstreamBillingProbeSettings(),
    );
  } catch (_error: unknown) {
    // Keep defaults when this optional setting cannot be loaded.
  } finally {
    upstreamBillingProbeLoading.value = false;
  }
}

async function saveUpstreamBillingProbeSettings() {
  upstreamBillingProbeSaving.value = true;
  try {
    const updated = await adminAPI.accounts.updateUpstreamBillingProbeSettings({
      ...upstreamBillingProbeForm,
    });
    Object.assign(upstreamBillingProbeForm, updated);
    appStore.showSuccess(t("admin.settings.upstreamBillingProbe.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        t("admin.settings.upstreamBillingProbe.saveFailed"),
      ),
    );
  } finally {
    upstreamBillingProbeSaving.value = false;
  }
}

async function loadOllamaCloudUsageSettings() {
  ollamaCloudUsageLoading.value = true;
  try {
    Object.assign(
      ollamaCloudUsageForm,
      await adminAPI.accounts.getOllamaCloudUsageSettings(),
    );
  } catch (_error: unknown) {
    // Keep the fail-safe disabled defaults when this optional setting cannot be loaded.
  } finally {
    ollamaCloudUsageLoading.value = false;
  }
}

async function saveOllamaCloudUsageSettings() {
  ollamaCloudUsageSaving.value = true;
  try {
    const updated = await adminAPI.accounts.updateOllamaCloudUsageSettings({
      ...ollamaCloudUsageForm,
    });
    Object.assign(ollamaCloudUsageForm, updated);
    appStore.showSuccess(t("admin.settings.ollamaCloudUsage.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.ollamaCloudUsage.saveFailed")),
    );
  } finally {
    ollamaCloudUsageSaving.value = false;
  }
}

// Overload Cooldown 方法
async function loadOverloadCooldownSettings() {
  overloadCooldownLoading.value = true;
  try {
    const settings = await adminAPI.settings.getOverloadCooldownSettings();
    Object.assign(overloadCooldownForm, settings);
  } catch (_error: unknown) {
    // Silent fail - settings will use defaults
  } finally {
    overloadCooldownLoading.value = false;
  }
}

async function saveOverloadCooldownSettings() {
  overloadCooldownSaving.value = true;
  try {
    const updated = await adminAPI.settings.updateOverloadCooldownSettings({
      enabled: overloadCooldownForm.enabled,
      cooldown_minutes: overloadCooldownForm.cooldown_minutes,
    });
    Object.assign(overloadCooldownForm, updated);
    appStore.showSuccess(t("admin.settings.overloadCooldown.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        t("admin.settings.overloadCooldown.saveFailed"),
      ),
    );
  } finally {
    overloadCooldownSaving.value = false;
  }
}

// Panel API Rate Limit 方法
async function loadPanelRateLimitSettings() {
  panelRateLimitLoading.value = true;
  try {
    const settings = await adminAPI.settings.getPanelRateLimitSettings();
    Object.assign(panelRateLimitForm, settings);
  } catch (_error: unknown) {
    // Silent fail - settings will use defaults
  } finally {
    panelRateLimitLoading.value = false;
  }
}

async function savePanelRateLimitSettings() {
  panelRateLimitSaving.value = true;
  try {
    const updated = await adminAPI.settings.updatePanelRateLimitSettings({
      enabled: panelRateLimitForm.enabled,
      user_rpm: panelRateLimitForm.user_rpm,
      heavy_rpm: panelRateLimitForm.heavy_rpm,
      exempt_admin: panelRateLimitForm.exempt_admin,
      public_ip_rpm: panelRateLimitForm.public_ip_rpm,
    });
    Object.assign(panelRateLimitForm, updated);
    appStore.showSuccess(t("admin.settings.panelRateLimit.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        t("admin.settings.panelRateLimit.saveFailed"),
      ),
    );
  } finally {
    panelRateLimitSaving.value = false;
  }
}

// Rate Limit Cooldown (429) 方法
async function loadRateLimit429CooldownSettings() {
  rateLimit429CooldownLoading.value = true;
  try {
    const settings = await adminAPI.settings.getRateLimit429CooldownSettings();
    Object.assign(rateLimit429CooldownForm, settings);
  } catch (_error: unknown) {
    // Silent fail - settings will use defaults
  } finally {
    rateLimit429CooldownLoading.value = false;
  }
}

async function saveRateLimit429CooldownSettings() {
  rateLimit429CooldownSaving.value = true;
  try {
    const updated = await adminAPI.settings.updateRateLimit429CooldownSettings({
      enabled: rateLimit429CooldownForm.enabled,
      cooldown_seconds: rateLimit429CooldownForm.cooldown_seconds,
    });
    Object.assign(rateLimit429CooldownForm, updated);
    appStore.showSuccess(t("admin.settings.rateLimit429Cooldown.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        t("admin.settings.rateLimit429Cooldown.saveFailed"),
      ),
    );
  } finally {
    rateLimit429CooldownSaving.value = false;
  }
}

// Stream Timeout 方法
async function loadStreamTimeoutSettings() {
  streamTimeoutLoading.value = true;
  try {
    const settings = await adminAPI.settings.getStreamTimeoutSettings();
    Object.assign(streamTimeoutForm, settings);
  } catch (_error: unknown) {
    // Silent fail - settings will use defaults
  } finally {
    streamTimeoutLoading.value = false;
  }
}

async function saveStreamTimeoutSettings() {
  streamTimeoutSaving.value = true;
  try {
    const updated = await adminAPI.settings.updateStreamTimeoutSettings({
      enabled: streamTimeoutForm.enabled,
      action: streamTimeoutForm.action,
      temp_unsched_minutes: streamTimeoutForm.temp_unsched_minutes,
      threshold_count: streamTimeoutForm.threshold_count,
      threshold_window_minutes: streamTimeoutForm.threshold_window_minutes,
    });
    Object.assign(streamTimeoutForm, updated);
    appStore.showSuccess(t("admin.settings.streamTimeout.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        t("admin.settings.streamTimeout.saveFailed"),
      ),
    );
  } finally {
    streamTimeoutSaving.value = false;
  }
}

// Rectifier 方法
async function loadRectifierSettings() {
  rectifierLoading.value = true;
  try {
    const settings = await adminAPI.settings.getRectifierSettings();
    Object.assign(rectifierForm, settings);
    // 确保 patterns 是数组（旧数据可能为 null）
    if (!Array.isArray(rectifierForm.apikey_signature_patterns)) {
      rectifierForm.apikey_signature_patterns = [];
    }
  } catch (_error: unknown) {
    // Silent fail - settings will use defaults
  } finally {
    rectifierLoading.value = false;
  }
}

async function saveRectifierSettings() {
  rectifierSaving.value = true;
  try {
    const updated = await adminAPI.settings.updateRectifierSettings({
      enabled: rectifierForm.enabled,
      thinking_signature_enabled: rectifierForm.thinking_signature_enabled,
      thinking_budget_enabled: rectifierForm.thinking_budget_enabled,
      apikey_signature_enabled: rectifierForm.apikey_signature_enabled,
      apikey_signature_patterns: rectifierForm.apikey_signature_patterns.filter(
        (p) => p.trim() !== "",
      ),
    });
    Object.assign(rectifierForm, updated);
    if (!Array.isArray(rectifierForm.apikey_signature_patterns)) {
      rectifierForm.apikey_signature_patterns = [];
    }
    appStore.showSuccess(t("admin.settings.rectifier.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.rectifier.saveFailed")),
    );
  } finally {
    rectifierSaving.value = false;
  }
}

const betaPolicyActionOptions = computed(() => [
  { value: "pass", label: t("admin.settings.betaPolicy.actionPass") },
  { value: "filter", label: t("admin.settings.betaPolicy.actionFilter") },
  { value: "block", label: t("admin.settings.betaPolicy.actionBlock") },
]);

const betaPolicyScopeOptions = computed(() => [
  { value: "all", label: t("admin.settings.betaPolicy.scopeAll") },
  { value: "oauth", label: t("admin.settings.betaPolicy.scopeOAuth") },
  { value: "apikey", label: t("admin.settings.betaPolicy.scopeAPIKey") },
  { value: "bedrock", label: t("admin.settings.betaPolicy.scopeBedrock") },
]);

// Beta Policy 方法
const betaDisplayNames: Record<string, string> = {
  "fast-mode-2026-02-01": "Fast Mode",
  "context-1m-2025-08-07": "Context 1M",
};

// 快捷预设：按 beta_token 定义预设方案
const betaPresets: Record<
  string,
  Array<{
    label: string;
    description: string;
    action: "pass" | "filter" | "block";
    model_whitelist: string[];
    fallback_action: "pass" | "filter" | "block";
  }>
> = {
  "context-1m-2025-08-07": [
    {
      label: t("admin.settings.betaPolicy.presetOpusOnly"),
      description: t("admin.settings.betaPolicy.presetOpusOnlyDesc"),
      action: "pass",
      model_whitelist: ["claude-opus-4-6"],
      fallback_action: "filter",
    },
  ],
};

// 常用模型模式（具体 ID + 通配符示例）
const commonModelPatterns = [
  "claude-opus-4-6",
  "claude-sonnet-4-6",
  "claude-opus-*",
  "claude-sonnet-*",
];

function getBetaDisplayName(token: string): string {
  return betaDisplayNames[token] || token;
}

function applyBetaPreset(
  rule: (typeof betaPolicyForm.rules)[number],
  preset: {
    action: "pass" | "filter" | "block";
    model_whitelist: string[];
    fallback_action: "pass" | "filter" | "block";
  },
) {
  rule.action = preset.action;
  rule.model_whitelist = [...preset.model_whitelist];
  rule.fallback_action = preset.fallback_action;
}

function addQuickPattern(
  rule: (typeof betaPolicyForm.rules)[number],
  pattern: string,
) {
  if (!rule.model_whitelist) rule.model_whitelist = [];
  if (!rule.model_whitelist.includes(pattern)) {
    rule.model_whitelist.push(pattern);
  }
}

async function loadBetaPolicySettings() {
  betaPolicyLoading.value = true;
  try {
    const settings = await adminAPI.settings.getBetaPolicySettings();
    betaPolicyForm.rules = settings.rules;
  } catch (_error: unknown) {
    // Silent fail - settings will use defaults
  } finally {
    betaPolicyLoading.value = false;
  }
}

// ==================== OpenAI Fast/Flex Policy ====================

const openaiFastPolicyTierOptions = computed(() => [
  { value: "all", label: t("admin.settings.openaiFastPolicy.tierAll") },
  {
    value: "priority",
    label: t("admin.settings.openaiFastPolicy.tierPriority"),
  },
  {
    value: "ultrafast",
    label: t("admin.settings.openaiFastPolicy.tierUltrafast"),
  },
  { value: "flex", label: t("admin.settings.openaiFastPolicy.tierFlex") },
  { value: "missing", label: t("admin.settings.openaiFastPolicy.tierMissing") },
]);

const openaiFastPolicyActionOptions = computed(() => [
  { value: "pass", label: t("admin.settings.openaiFastPolicy.actionPass") },
  { value: "filter", label: t("admin.settings.openaiFastPolicy.actionFilter") },
  {
    value: "force_priority",
    label: t("admin.settings.openaiFastPolicy.actionForcePriority"),
  },
  { value: "block", label: t("admin.settings.openaiFastPolicy.actionBlock") },
]);

function openaiFastPolicyActionSummary(
  action: OpenAIFastPolicyRule["action"],
) {
  return t(`admin.settings.openaiFastPolicy.summaryAction.${action}`);
}

function hasOpenAIFastPolicyTargetModels(rule: OpenAIFastPolicyRule) {
  return Boolean(rule.model_whitelist?.some((pattern) => pattern.trim() !== ""));
}

const openaiFastPolicyScopeOptions = computed(() => [
  { value: "all", label: t("admin.settings.openaiFastPolicy.scopeAll") },
  { value: "oauth", label: t("admin.settings.openaiFastPolicy.scopeOAuth") },
  { value: "apikey", label: t("admin.settings.openaiFastPolicy.scopeAPIKey") },
  {
    value: "bedrock",
    label: t("admin.settings.openaiFastPolicy.scopeBedrock"),
  },
]);

function addOpenAIFastPolicyRule() {
  openaiFastPolicyForm.rules.push({
    service_tier: "priority",
    action: "filter",
    scope: "all",
    user_ids: [],
    error_message: "",
    model_whitelist: [],
    fallback_action: "pass",
    fallback_error_message: "",
  });
}

function removeOpenAIFastPolicyRule(index: number) {
  openaiFastPolicyForm.rules.splice(index, 1);
}

function addOpenAIFastPolicyModelPattern(rule: OpenAIFastPolicyRule) {
  if (!rule.model_whitelist) rule.model_whitelist = [];
  rule.model_whitelist.push("");
}

function removeOpenAIFastPolicyModelPattern(
  rule: OpenAIFastPolicyRule,
  idx: number,
) {
  rule.model_whitelist?.splice(idx, 1);
}

async function saveBetaPolicySettings() {
  betaPolicySaving.value = true;
  try {
    // Clean up empty patterns before saving
    const cleanedRules = betaPolicyForm.rules.map((rule) => {
      const whitelist = rule.model_whitelist?.filter((p) => p.trim() !== "");
      const hasWhitelist = whitelist && whitelist.length > 0;
      return {
        beta_token: rule.beta_token,
        action: rule.action,
        scope: rule.scope,
        error_message: rule.error_message,
        model_whitelist: hasWhitelist ? whitelist : undefined,
        fallback_action: hasWhitelist
          ? rule.fallback_action || "pass"
          : undefined,
        fallback_error_message:
          hasWhitelist && rule.fallback_action === "block"
            ? rule.fallback_error_message
            : undefined,
      };
    });
    const updated = await adminAPI.settings.updateBetaPolicySettings({
      rules: cleanedRules,
    });
    betaPolicyForm.rules = updated.rules;
    appStore.showSuccess(t("admin.settings.betaPolicy.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.betaPolicy.saveFailed")),
    );
  } finally {
    betaPolicySaving.value = false;
  }
}

// ==================== Provider Management ====================

const allPaymentTypes = computed(() => [
  { value: "easypay", label: t("payment.methods.easypay") },
  { value: "alipay", label: t("payment.methods.alipay") },
  { value: "wxpay", label: t("payment.methods.wxpay") },
  { value: "stripe", label: t("payment.methods.stripe") },
  { value: "airwallex", label: t("payment.methods.airwallex") },
  { value: "epusdt", label: t("payment.methods.epusdt") },
]);

function isPaymentTypeEnabled(type: string): boolean {
  return form.payment_enabled_types.includes(type);
}

const hasAnyPaymentTypeEnabled = computed(
  () => form.payment_enabled_types.length > 0,
);

function togglePaymentType(type: string) {
  if (form.payment_enabled_types.includes(type)) {
    form.payment_enabled_types = form.payment_enabled_types.filter(
      (t) => t !== type,
    );
    // Disable all provider instances matching this type
    disableProvidersByType(type);
  } else {
    form.payment_enabled_types = [...form.payment_enabled_types, type];
  }
}

async function disableProvidersByType(type: string) {
  const matching = providers.value.filter(
    (p) => p.provider_key === type && p.enabled,
  );
  for (const p of matching) {
    try {
      await adminAPI.payment.updateProvider(p.id, { enabled: false });
      p.enabled = false;
    } catch (err: unknown) {
      slog("disable provider failed", p.id, err);
    }
  }
}

function slog(...args: unknown[]) {
  console.warn("[payment]", ...args);
}

const providersLoading = ref(false);
const providerSaving = ref(false);
const providers = ref<ProviderInstance[]>([]);
const showProviderDialog = ref(false);
const showDeleteProviderDialog = ref(false);
const editingProvider = ref<ProviderInstance | null>(null);
const deletingProviderId = ref<number | null>(null);
const providerDialogRef = ref<InstanceType<
  typeof PaymentProviderDialog
> | null>(null);

const providerKeyOptions = computed(() => [
  { value: "easypay", label: t("admin.settings.payment.providerEasypay") },
  { value: "alipay", label: t("admin.settings.payment.providerAlipay") },
  { value: "wxpay", label: t("admin.settings.payment.providerWxpay") },
  { value: "stripe", label: t("admin.settings.payment.providerStripe") },
  { value: "airwallex", label: t("admin.settings.payment.providerAirwallex") },
  { value: "epusdt", label: t("admin.settings.payment.providerEpusdt") },
]);

const enabledProviderKeyOptions = computed(() => {
  const enabled = form.payment_enabled_types;
  return providerKeyOptions.value.filter((opt) => enabled.includes(opt.value));
});

const loadBalanceOptions = computed(() => [
  {
    value: "round-robin",
    label: t("admin.settings.payment.strategyRoundRobin"),
  },
  {
    value: "least-amount",
    label: t("admin.settings.payment.strategyLeastAmount"),
  },
]);

const cancelRateLimitUnitOptions = computed(() => [
  {
    value: "minute",
    label: t("admin.settings.payment.cancelRateLimitUnitMinute"),
  },
  { value: "hour", label: t("admin.settings.payment.cancelRateLimitUnitHour") },
  { value: "day", label: t("admin.settings.payment.cancelRateLimitUnitDay") },
]);

const cancelRateLimitModeOptions = computed(() => [
  {
    value: "rolling",
    label: t("admin.settings.payment.cancelRateLimitWindowModeRolling"),
  },
  {
    value: "fixed",
    label: t("admin.settings.payment.cancelRateLimitWindowModeFixed"),
  },
]);

type ProviderEnablementCandidate = Pick<
  ProviderInstance,
  "id" | "provider_key" | "supported_types" | "enabled" | "name"
>;

function getProviderVisibleMethods(
  provider: ProviderEnablementCandidate,
): Array<"alipay" | "wxpay"> {
  if (!provider.enabled) {
    return [];
  }

  const supportedTypes = Array.isArray(provider.supported_types)
    ? provider.supported_types
    : [];
  const methods = new Set<"alipay" | "wxpay">();
  const addMethod = (type: string) => {
    const method = normalizeVisibleMethod(type);
    if (method === "alipay" || method === "wxpay") {
      methods.add(method);
    }
  };

  if (provider.provider_key === "alipay") {
    if (supportedTypes.length === 0) {
      methods.add("alipay");
    } else {
      supportedTypes.forEach((type) => {
        if (normalizeVisibleMethod(type) === "alipay") {
          methods.add("alipay");
        }
      });
    }
  } else if (provider.provider_key === "wxpay") {
    if (supportedTypes.length === 0) {
      methods.add("wxpay");
    } else {
      supportedTypes.forEach((type) => {
        if (normalizeVisibleMethod(type) === "wxpay") {
          methods.add("wxpay");
        }
      });
    }
  } else if (provider.provider_key === "easypay") {
    supportedTypes.forEach(addMethod);
  }

  return Array.from(methods);
}

function findProviderEnablementConflict(
  candidate: ProviderEnablementCandidate,
): { method: "alipay" | "wxpay"; conflicting: ProviderInstance } | null {
  const claimedMethods = getProviderVisibleMethods(candidate);
  if (claimedMethods.length === 0) {
    return null;
  }

  for (const other of providers.value) {
    if (other.id === candidate.id || !other.enabled) {
      continue;
    }

    const otherMethods = getProviderVisibleMethods(other);
    const matchedMethod = claimedMethods.find((method) =>
      otherMethods.includes(method),
    );
    if (matchedMethod) {
      return {
        method: matchedMethod,
        conflicting: other,
      };
    }
  }

  return null;
}

function showProviderEnablementConflict(
  conflict: { method: "alipay" | "wxpay"; conflicting: ProviderInstance },
) {
  appStore.showError(
    t("admin.settings.payment.enableConflict", {
      method: t(`payment.methods.${conflict.method}`),
      provider: conflict.conflicting.name,
    }),
  );
}

async function loadProviders() {
  providersLoading.value = true;
  try {
    const res = await adminAPI.payment.getProviders();
    // Normalize supported_types: backend returns null when the list is empty
    // (Go nil slice → JSON null). Without this, ProviderCard's isSelected()
    // throws TypeError on null.includes(), causing the card to vanish.
    providers.value = (res.data || []).map((p) => ({
      ...p,
      supported_types: Array.isArray(p.supported_types)
        ? p.supported_types
        : [],
    }));
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, "payment.errors", t("common.error")));
  } finally {
    providersLoading.value = false;
  }
}

function openCreateProvider() {
  editingProvider.value = null;
  providerDialogRef.value?.reset(
    enabledProviderKeyOptions.value[0]?.value || "easypay",
  );
  showProviderDialog.value = true;
}

function openEditProvider(provider: ProviderInstance) {
  editingProvider.value = provider;
  providerDialogRef.value?.loadProvider(provider);
  showProviderDialog.value = true;
}

async function handleSaveProvider(payload: Partial<ProviderInstance>) {
  providerSaving.value = true;
  try {
    const candidate: ProviderEnablementCandidate = {
      id: editingProvider.value?.id ?? 0,
      provider_key:
        payload.provider_key ?? editingProvider.value?.provider_key ?? "",
      supported_types:
        payload.supported_types ?? editingProvider.value?.supported_types ?? [],
      enabled: payload.enabled ?? editingProvider.value?.enabled ?? false,
      name: payload.name ?? editingProvider.value?.name ?? "",
    };
    const conflict = findProviderEnablementConflict(candidate);
    if (conflict) {
      showProviderEnablementConflict(conflict);
      return;
    }

    if (editingProvider.value) {
      await adminAPI.payment.updateProvider(editingProvider.value.id, payload);
    } else {
      await adminAPI.payment.createProvider(payload);
    }
    showProviderDialog.value = false;
    // Reload full list (API returns decrypted/formatted data with correct sort order)
    await loadProviders();
    // Auto-save settings so provider changes take effect immediately
    await saveSettings();
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, "payment.errors", t("common.error")));
  } finally {
    providerSaving.value = false;
  }
}

async function handleToggleField(
  provider: ProviderInstance,
  field: "enabled" | "refund_enabled" | "allow_user_refund",
) {
  let newValue: boolean;
  if (field === "enabled") newValue = !provider.enabled;
  else if (field === "refund_enabled") newValue = !provider.refund_enabled;
  else newValue = !provider.allow_user_refund;

  if (field === "enabled" && newValue) {
    const conflict = findProviderEnablementConflict({
      id: provider.id,
      provider_key: provider.provider_key,
      supported_types: provider.supported_types,
      enabled: true,
      name: provider.name,
    });
    if (conflict) {
      showProviderEnablementConflict(conflict);
      return;
    }
  }

  const payload: Record<string, boolean> = { [field]: newValue };
  // Cascade: turning off refund_enabled also turns off allow_user_refund
  if (field === "refund_enabled" && !newValue) {
    payload.allow_user_refund = false;
  }
  try {
    await adminAPI.payment.updateProvider(provider.id, payload);
    await loadProviders();
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, "payment.errors", t("common.error")));
  }
}

async function handleToggleType(provider: ProviderInstance, type: string) {
  const currentTypes = Array.isArray(provider.supported_types)
    ? provider.supported_types
    : [];
  const updated = currentTypes.includes(type)
    ? currentTypes.filter((t) => t !== type)
    : [...currentTypes, type];
  const conflict = findProviderEnablementConflict({
    id: provider.id,
    provider_key: provider.provider_key,
    supported_types: updated,
    enabled: provider.enabled,
    name: provider.name,
  });
  if (conflict) {
    showProviderEnablementConflict(conflict);
    return;
  }
  try {
    await adminAPI.payment.updateProvider(provider.id, {
      supported_types: updated,
    } as any);
    await loadProviders();
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, "payment.errors", t("common.error")));
  }
}

function confirmDeleteProvider(provider: ProviderInstance) {
  deletingProviderId.value = provider.id;
  showDeleteProviderDialog.value = true;
}

async function handleReorderProviders(
  updates: { id: number; sort_order: number }[],
) {
  try {
    await Promise.all(
      updates.map((u) =>
        adminAPI.payment.updateProvider(u.id, {
          sort_order: u.sort_order,
        } as Partial<ProviderInstance>),
      ),
    );
    await loadProviders();
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, "payment.errors", t("common.error")));
    loadProviders();
  }
}

async function handleDeleteProvider() {
  if (!deletingProviderId.value) return;
  try {
    await adminAPI.payment.deleteProvider(deletingProviderId.value);
    appStore.showSuccess(t("common.deleted"));
    showDeleteProviderDialog.value = false;
    loadProviders();
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, "payment.errors", t("common.error")));
  }
}

onMounted(() => {
  loadSettings();
  loadSubscriptionGroups();
  loadAdminApiKey();
  loadUpstreamBillingProbeSettings();
  loadOllamaCloudUsageSettings();
  loadOverloadCooldownSettings();
  loadRateLimit429CooldownSettings();
  loadPanelRateLimitSettings();
  loadStreamTimeoutSettings();
  loadRectifierSettings();
  loadBetaPolicySettings();
  loadProviders();
});

// =========================
// Affiliate (邀请返利) 专属用户管理
// =========================

interface AffiliateState {
  loading: boolean;
  entries: AffiliateAdminEntry[];
  total: number;
  page: number;
  pageSize: number;
  search: string;
  selected: number[];
  searchTimer: number | null;
}

const affiliateState = reactive<AffiliateState>({
  loading: false,
  entries: [],
  total: 0,
  page: 1,
  pageSize: 20,
  search: "",
  selected: [],
  searchTimer: null,
});

// `rate` is typed as string|number because a number UInput makes Vue's
// v-model auto-cast the bound value to a Number on every keystroke. We keep
// both shapes and normalize at read time.
interface AffiliateModalState {
  open: boolean;
  mode: "add" | "edit";
  saving: boolean;
  userQuery: string;
  userResults: AffiliateSimpleUser[];
  selectedUser: AffiliateSimpleUser | null;
  editingEntry: AffiliateAdminEntry | null;
  code: string;
  rate: string | number;
  searchTimer: number | null;
  // 额外邀请码（默认码之外的）
  codes: AdminUserAffiliateCode[];
  codesQuota: AdminUserAffiliateCodeQuota;
  defaultInviteCount: number;
  defaultRebateAmount: number;
  codesLoading: boolean;
  codesSaving: boolean;
  newCode: string;
}

const affiliateModal = reactive<AffiliateModalState>({
  open: false,
  mode: "add",
  saving: false,
  userQuery: "",
  userResults: [],
  selectedUser: null,
  editingEntry: null,
  code: "",
  rate: "",
  searchTimer: null,
  codes: [],
  codesQuota: { max: 9, used: 0, limit: 9 },
  defaultInviteCount: 0,
  defaultRebateAmount: 0,
  codesLoading: false,
  codesSaving: false,
  newCode: "",
});

const affiliateBatchModal = reactive<{
  open: boolean;
  saving: boolean;
  rate: string | number;
}>({
  open: false,
  saving: false,
  rate: "",
});

// affiliateConfirmDialog drives the project-standard <ConfirmDialog>. We can't
// `await` the user's response from the dialog component, so the confirm action
// runs from the @confirm callback once the user clicks the dialog's confirm
// button.
const affiliateConfirmDialog = reactive<{
  show: boolean;
  title: string;
  message: string;
  confirmText: string;
  pending: (() => Promise<unknown>) | null;
}>({
  show: false,
  title: "",
  message: "",
  confirmText: "",
  pending: null,
});

function openAffiliateConfirm(
  title: string,
  message: string,
  confirmText: string,
  fn: () => Promise<unknown>,
) {
  affiliateConfirmDialog.title = title;
  affiliateConfirmDialog.message = message;
  affiliateConfirmDialog.confirmText = confirmText;
  affiliateConfirmDialog.pending = fn;
  affiliateConfirmDialog.show = true;
}

async function handleAffiliateConfirm() {
  const fn = affiliateConfirmDialog.pending;
  affiliateConfirmDialog.show = false;
  affiliateConfirmDialog.pending = null;
  if (!fn) return;
  try {
    await fn();
    appStore.showSuccess(t("common.saved"));
    await loadAffiliateUsers();
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  }
}

function cancelAffiliateConfirm() {
  affiliateConfirmDialog.show = false;
  affiliateConfirmDialog.pending = null;
}

// debounceTimer wires a single timer slot to a callback with a delay,
// canceling any pending invocation. Used for type-as-you-go search inputs.
function debounceTimer(slot: { searchTimer: number | null }, delayMs: number, run: () => void) {
  if (slot.searchTimer != null) window.clearTimeout(slot.searchTimer);
  slot.searchTimer = window.setTimeout(run, delayMs);
}

// parseRebateRate validates 0-100 numeric input. Returns the parsed number on
// success, null when the field is empty (caller decides empty semantics), or
// undefined on invalid input (after surfacing a toast).
//
// Accepts unknown because a number UInput makes Vue's v-model coerce
// the value to Number on each keystroke (e.g. typing "30" lands a `30: number`
// in state, not a `"30": string`). String("") and (30).trim() would crash, so
// we normalize here instead of forcing every caller to remember.
function parseRebateRate(raw: unknown): number | null | undefined {
  const s = String(raw ?? "").trim();
  if (s === "") return null;
  const parsed = Number(s);
  if (Number.isNaN(parsed) || parsed < 0 || parsed > 100) {
    appStore.showError(t("admin.settings.features.affiliate.modal.errorBadRate"));
    return undefined;
  }
  return parsed;
}

async function loadAffiliateUsers() {
  affiliateState.loading = true;
  try {
    const res = await affiliatesAPI.listUsers({
      page: affiliateState.page,
      page_size: affiliateState.pageSize,
      search: affiliateState.search,
    });
    affiliateState.entries = res.items ?? [];
    affiliateState.total = res.total ?? 0;
    if (res.page > 0) affiliateState.page = res.page;
    if (res.page_size > 0) affiliateState.pageSize = res.page_size;
    // Drop selections that are no longer visible.
    const visibleIds = new Set(affiliateState.entries.map((e) => e.user_id));
    affiliateState.selected = affiliateState.selected.filter((id) => visibleIds.has(id));
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  } finally {
    affiliateState.loading = false;
  }
}

function onAffiliateSearchInput() {
  debounceTimer(affiliateState, 300, () => {
    affiliateState.page = 1;
    loadAffiliateUsers();
  });
}

function changeAffiliatePage(page: number) {
  if (page < 1) return;
  affiliateState.page = page;
  loadAffiliateUsers();
}

const affiliateTableColumns = computed<TableColumn<AffiliateAdminEntry>[]>(() => [
  { id: "select" },
  { accessorKey: "email", header: t("admin.settings.features.affiliate.customUsers.col.email") },
  { accessorKey: "username", header: t("admin.settings.features.affiliate.customUsers.col.username") },
  { accessorKey: "aff_code", header: t("admin.settings.features.affiliate.customUsers.col.code") },
  { id: "rate", header: t("admin.settings.features.affiliate.customUsers.col.rate") },
  { id: "actions", header: t("admin.settings.features.affiliate.customUsers.col.actions") },
]);

// UCheckbox emits the new value (true / false / "indeterminate"), not an Event.
function toggleAffiliateSelectAll(checked: boolean | "indeterminate") {
  affiliateState.selected = checked === true ? affiliateState.entries.map((entry) => entry.user_id) : [];
}

function toggleAffiliateSelect(userId: number) {
  const idx = affiliateState.selected.indexOf(userId);
  if (idx >= 0) affiliateState.selected.splice(idx, 1);
  else affiliateState.selected.push(userId);
}

// openAffiliateModal opens the add/edit modal, prefilling fields from the
// edited entry when present and resetting them otherwise.
function openAffiliateModal(entry: AffiliateAdminEntry | null) {
  affiliateModal.open = true;
  affiliateModal.mode = entry ? "edit" : "add";
  affiliateModal.userQuery = "";
  affiliateModal.userResults = [];
  affiliateModal.selectedUser = null;
  affiliateModal.editingEntry = entry;
  affiliateModal.code = entry?.aff_code_custom ? entry.aff_code : "";
  affiliateModal.rate =
    entry?.aff_rebate_rate_percent != null ? String(entry.aff_rebate_rate_percent) : "";
  affiliateModal.codes = [];
  affiliateModal.codesQuota = { max: 9, used: 0, limit: 9 };
  affiliateModal.defaultInviteCount = 0;
  affiliateModal.defaultRebateAmount = 0;
  affiliateModal.codesLoading = false;
  affiliateModal.codesSaving = false;
  affiliateModal.newCode = "";
  if (entry) {
    void loadAffiliateCodes(entry.user_id);
  }
}

async function loadAffiliateCodes(userId: number) {
  affiliateModal.codesLoading = true;
  try {
    const overview = await affiliatesAPI.getUserOverview(userId);
    affiliateModal.codes = overview.custom_codes ?? [];
    affiliateModal.codesQuota = overview.custom_code_quota ?? { max: 9, used: 0, limit: 9 };
    affiliateModal.defaultInviteCount = overview.aff_code_invite_count ?? 0;
    affiliateModal.defaultRebateAmount = overview.aff_code_rebate_amount ?? 0;
  } catch (err) {
    appStore.showError(
      extractApiErrorMessage(err, t("admin.settings.features.affiliate.codes.loadFailed")),
    );
  } finally {
    affiliateModal.codesLoading = false;
  }
}

async function addAffiliateCode() {
  const entry = affiliateModal.editingEntry;
  const code = affiliateModal.newCode.trim().toUpperCase();
  if (!entry || !code || affiliateModal.codesSaving) return;
  if (!/^[A-Z0-9_-]{4,32}$/.test(code)) {
    appStore.showError(t("admin.settings.features.affiliate.codes.invalidFormat"));
    return;
  }
  affiliateModal.codesSaving = true;
  try {
    await affiliatesAPI.createUserAffiliateCode(entry.user_id, code);
    affiliateModal.newCode = "";
    appStore.showSuccess(t("admin.settings.features.affiliate.codes.createSuccess"));
    await loadAffiliateCodes(entry.user_id);
  } catch (err) {
    appStore.showError(
      extractApiErrorMessage(err, t("admin.settings.features.affiliate.codes.createFailed")),
    );
  } finally {
    affiliateModal.codesSaving = false;
  }
}

function removeAffiliateCode(code: string) {
  const entry = affiliateModal.editingEntry;
  if (!entry || affiliateModal.codesSaving) return;
  openAffiliateConfirm(
    t("admin.settings.features.affiliate.codes.remove"),
    t("admin.settings.features.affiliate.codes.removeConfirm", { code }),
    t("admin.settings.features.affiliate.codes.remove"),
    async () => {
      affiliateModal.codesSaving = true;
      try {
        await affiliatesAPI.deleteUserAffiliateCode(entry.user_id, code);
        appStore.showSuccess(t("admin.settings.features.affiliate.codes.deleteSuccess"));
        await loadAffiliateCodes(entry.user_id);
      } catch (err) {
        appStore.showError(
          extractApiErrorMessage(err, t("admin.settings.features.affiliate.codes.deleteFailed")),
        );
      } finally {
        affiliateModal.codesSaving = false;
      }
    },
  );
}

function closeAffiliateModal() {
  affiliateModal.open = false;
  if (affiliateModal.searchTimer != null) {
    window.clearTimeout(affiliateModal.searchTimer);
    affiliateModal.searchTimer = null;
  }
}

function onAffiliateUserSearchInput() {
  const q = affiliateModal.userQuery.trim();
  if (!q) {
    affiliateModal.userResults = [];
    return;
  }
  debounceTimer(affiliateModal, 300, async () => {
    try {
      affiliateModal.userResults = await affiliatesAPI.lookupUsers(q);
    } catch (err) {
      appStore.showError(extractApiErrorMessage(err, t("common.error")));
    }
  });
}

// selectAffiliateUser picks a user from the dropdown and collapses the search
// UI. Clearing the result list also clears the visual dropdown.
function selectAffiliateUser(user: AffiliateSimpleUser) {
  affiliateModal.selectedUser = user;
  affiliateModal.userQuery = "";
  affiliateModal.userResults = [];
}

function clearSelectedAffiliateUser() {
  affiliateModal.selectedUser = null;
}

// affiliateModalCanSubmit guards the Save button: must have a user picked AND
// produce at least one field change. Without this the admin could "save" an
// empty payload that silently does nothing — the user reported exactly that
// confusion.
const affiliateModalCanSubmit = computed(() => {
  if (affiliateModal.mode === "add") {
    if (!affiliateModal.selectedUser) return false;
  } else if (!affiliateModal.editingEntry) {
    return false;
  }
  const codeFilled = affiliateModal.code.trim() !== "";
  const rateFilled = String(affiliateModal.rate ?? "").trim() !== "";
  if (codeFilled || rateFilled) return true;
  // Edit mode + empty rate input is a meaningful "clear" only if the user
  // currently has an exclusive rate to clear.
  return (
    affiliateModal.mode === "edit" &&
    affiliateModal.editingEntry?.aff_rebate_rate_percent != null
  );
});

async function submitAffiliateModal() {
  if (!affiliateModalCanSubmit.value) {
    // Should be unreachable because the button is disabled, but keep a guard.
    appStore.showError(t("admin.settings.features.affiliate.modal.errorEmpty"));
    return;
  }

  let userId: number;
  if (affiliateModal.mode === "add") {
    userId = affiliateModal.selectedUser!.id;
  } else {
    userId = affiliateModal.editingEntry!.user_id;
  }

  const payload: Parameters<typeof affiliatesAPI.updateUserSettings>[1] = {};
  const codeRaw = affiliateModal.code.trim();
  if (codeRaw) payload.aff_code = codeRaw.toUpperCase();

  const rateInput = parseRebateRate(affiliateModal.rate);
  if (rateInput === undefined) return; // toast already shown
  if (rateInput === null) {
    if (affiliateModal.mode === "edit" && affiliateModal.editingEntry?.aff_rebate_rate_percent != null) {
      payload.clear_rebate_rate = true;
    }
  } else {
    payload.aff_rebate_rate_percent = rateInput;
  }

  affiliateModal.saving = true;
  try {
    await affiliatesAPI.updateUserSettings(userId, payload);
    appStore.showSuccess(t("common.saved"));
    closeAffiliateModal();
    affiliateState.page = 1;
    await loadAffiliateUsers();
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  } finally {
    affiliateModal.saving = false;
  }
}

// askResetAffiliateUser prompts via the project ConfirmDialog, then on confirm
// calls the backend "reset all" endpoint that clears both the exclusive rate
// AND regenerates the invite code as a system random one.
function askResetAffiliateUser(entry: AffiliateAdminEntry) {
  openAffiliateConfirm(
    t("admin.settings.features.affiliate.customUsers.resetTitle"),
    t("admin.settings.features.affiliate.customUsers.resetMessage", {
      email: entry.email || `#${entry.user_id}`,
    }),
    t("common.delete"),
    () => affiliatesAPI.clearUserSettings(entry.user_id),
  );
}

function openAffiliateBatchModal() {
  if (affiliateState.selected.length === 0) return;
  affiliateBatchModal.open = true;
  affiliateBatchModal.rate = "";
}

async function submitAffiliateBatchModal() {
  const rateInput = parseRebateRate(affiliateBatchModal.rate);
  if (rateInput === undefined) return;
  const userIDs = [...affiliateState.selected];
  const payload: Parameters<typeof affiliatesAPI.batchSetRate>[0] =
    rateInput === null
      ? { user_ids: userIDs, clear: true }
      : { user_ids: userIDs, aff_rebate_rate_percent: rateInput };

  affiliateBatchModal.saving = true;
  try {
    await affiliatesAPI.batchSetRate(payload);
    appStore.showSuccess(t("common.saved"));
    affiliateBatchModal.open = false;
    affiliateState.selected = [];
    await loadAffiliateUsers();
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  } finally {
    affiliateBatchModal.saving = false;
  }
}

// Load the per-user table the first time the affiliate switch is observed
// as enabled. The form starts disabled and is updated to the server's value
// after the settings load — so this fires either when the saved value is
// truthy on first paint, or when the admin manually toggles it on.
watch(
  () => form.affiliate_enabled,
  (enabled, prev) => {
    if (enabled && !prev) {
      loadAffiliateUsers();
    }
  },
);

// bypass_registration 与身份同步三开关仅在 internal_only 模式下生效。切换 policy 到其它值时，
// 立即把相关字段重置为 false，避免保存请求里残留旧值。后端 admin handler 与
// 配置加载层都有 coerce 兜底，这里是 UX 层的同步而非安全防线。
watch(
  () => form.dingtalk_connect_corp_restriction_policy,
  (policy) => {
    if (policy !== "internal_only") {
      if (form.dingtalk_connect_bypass_registration) form.dingtalk_connect_bypass_registration = false;
      if (form.dingtalk_connect_sync_corp_email) form.dingtalk_connect_sync_corp_email = false;
      if (form.dingtalk_connect_sync_display_name) form.dingtalk_connect_sync_display_name = false;
      if (form.dingtalk_connect_sync_dept) form.dingtalk_connect_sync_dept = false;
    }
  },
);
</script>

<style scoped>
@reference "../../style.css";
/* Command-palette deep links briefly outline the section they jump to. */
.settings-section-highlight {
  @apply rounded-2xl ring-2 ring-primary/60;
}
.default-sub-group-select :deep(.select-trigger) {
  @apply h-[42px];
}

.default-sub-delete-btn {
  @apply h-[42px];
}

/* ============ 系统设置 Tab 导航 ============ */
.settings-tabs-shell {
  @apply sticky z-20 -mx-1 rounded-2xl border border-white/80 bg-white/90 p-1.5 backdrop-blur-xl;
  top: 4.75rem;
  box-shadow:
    0 12px 28px rgb(15 23 42 / 0.07),
    0 1px 0 rgb(255 255 255 / 0.9) inset;
}

.settings-toolbar {
  min-width: 0;
}

@media (max-width: 767px) {
  .settings-toolbar-tabs {
    flex-basis: 100%;
    width: 100%;
  }

  .settings-toolbar-save {
    min-height: 40px;
  }
}

.settings-tabs-scroll {
  @apply overflow-x-auto;
  -ms-overflow-style: none;
  scrollbar-width: none;
}

.settings-tabs-scroll::-webkit-scrollbar {
  display: none;
}

.settings-tabs {
  @apply flex min-w-max items-center gap-1;
}

.settings-tab {
  @apply relative isolate flex h-10 min-w-27 shrink-0 items-center justify-center gap-1.5 whitespace-nowrap rounded-xl border border-transparent px-3 text-sm font-medium text-gray-600 outline-hidden transition-colors duration-200 ease-out dark:text-gray-300;
}

@media (min-width: 768px) {
  .settings-tabs {
    @apply min-w-full;
  }

  .settings-tab {
    @apply min-w-0 flex-1 basis-0 overflow-hidden px-2 text-[13px];
  }

  .settings-tab-icon {
    @apply h-6 w-6;
  }
}

.settings-tab::before {
  @apply absolute inset-0 -z-10 rounded-xl opacity-0 transition-opacity duration-200;
  content: "";
  background: linear-gradient(135deg, rgb(248 250 252 / 0.95), rgb(241 245 249 / 0.8));
}

.settings-tab:hover::before,
.settings-tab:focus-visible::before {
  opacity: 1;
}

.settings-tab:focus-visible {
  @apply ring-2 ring-primary-500/40 ring-offset-2 ring-offset-white dark:ring-offset-dark-900;
}

.settings-tab-active {
  @apply border-primary-200/80 bg-white text-primary-700 shadow-xs dark:border-primary-400/30 dark:bg-dark-700/95 dark:text-primary-200;
  box-shadow:
    0 8px 18px rgb(15 23 42 / 0.08),
    0 1px 0 rgb(255 255 255 / 0.92) inset;
}

.settings-tab-active::before {
  opacity: 0;
}

.settings-tab-active::after {
  position: absolute;
  right: 0.75rem;
  bottom: 0.25rem;
  left: 0.75rem;
  height: 2px;
  border-radius: 9999px;
  content: "";
  background: linear-gradient(90deg, #14b8a6, #0ea5e9);
}

.settings-tab-icon {
  @apply flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-gray-500 transition-colors duration-200 dark:text-gray-400;
}

.settings-tab:hover .settings-tab-icon,
.settings-tab:focus-visible .settings-tab-icon {
  @apply text-gray-700 dark:text-gray-200;
}

.settings-tab-active .settings-tab-icon {
  @apply bg-primary-50 text-primary-600 dark:bg-primary-400/10 dark:text-primary-300;
}

.settings-tab-label {
  @apply min-w-0 overflow-hidden text-ellipsis whitespace-nowrap leading-none;
}
</style>

<style>
/* Dark-mode overrides for the settings tabs shell. Kept in an UNSCOPED block
   because Vue's scoped-CSS compiler was dropping the `:global(.dark) ...`
   rules in the production build, leaving inactive tabs unreadable on dark. */
.dark .settings-tabs-shell {
  border-color: rgb(51 65 85 / 0.65);
  background: rgb(15 23 42 / 0.86);
  box-shadow:
    0 16px 36px rgb(0 0 0 / 0.28),
    0 1px 0 rgb(255 255 255 / 0.06) inset;
}

.dark .settings-tab::before {
  background: linear-gradient(135deg, rgb(30 41 59 / 0.9), rgb(51 65 85 / 0.62));
}

.dark .settings-tab-active {
  box-shadow:
    0 12px 26px rgb(0 0 0 / 0.22),
    0 1px 0 rgb(255 255 255 / 0.08) inset;
}
</style>


