<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.createAccount')"
    width="wide"
    @close="handleClose"
  >
    <!-- Step indicator: platform/type → credentials → models/schedule → limits/network → confirm (→ OAuth) -->
    <UStepper
      :items="wizardStepperItems"
      :model-value="wizardStepperValue"
      :linear="false"
      size="sm"
      class="mb-6"
      @update:model-value="onWizardStepperSelect"
    />

    <!-- Step 1: Basic Info -->
    <form
      v-if="step === 1"
      id="create-account-form"
      ref="createFormRef"
      @submit.prevent="handleSubmit"
      @invalid.capture="onWizardInvalid"
      class="space-y-6"
    >

      <div v-show="showWizardStep(0)" data-wizard-step="0" class="space-y-6">
        <!-- Platform Selection - Segmented Control Style -->
        <div>
          <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.platform') }}</label>
          <div class="mt-2 grid grid-cols-2 gap-2 sm:grid-cols-5" data-tour="account-form-platform">
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="form.platform === 'anthropic'"
              :aria-pressed="form.platform === 'anthropic'"
              title="Anthropic"
              :ui="choiceCardUi"
              @click="form.platform = 'anthropic'"
            >
              <template #leading><PlatformIcon platform="anthropic" size="lg" /></template>
            </UPageCard>
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="form.platform === 'openai'"
              :aria-pressed="form.platform === 'openai'"
              title="OpenAI"
              :ui="choiceCardUi"
              @click="form.platform = 'openai'"
            >
              <template #leading><PlatformIcon platform="openai" size="lg" /></template>
            </UPageCard>
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="form.platform === 'gemini'"
              :aria-pressed="form.platform === 'gemini'"
              title="Gemini"
              :ui="choiceCardUi"
              @click="form.platform = 'gemini'"
            >
              <template #leading><PlatformIcon platform="gemini" size="lg" /></template>
            </UPageCard>
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="form.platform === 'antigravity'"
              :aria-pressed="form.platform === 'antigravity'"
              title="Antigravity"
              :ui="choiceCardUi"
              @click="form.platform = 'antigravity'"
            >
              <template #leading><PlatformIcon platform="antigravity" size="lg" /></template>
            </UPageCard>
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="form.platform === 'grok'"
              :aria-pressed="form.platform === 'grok'"
              title="Grok"
              :ui="choiceCardUi"
              @click="form.platform = 'grok'"
            >
              <template #leading><PlatformIcon platform="grok" size="lg" /></template>
            </UPageCard>
          </div>
          <!-- CN providers row: Kimi / Zhipu GLM / DeepSeek -->
          <div class="mt-2 grid grid-cols-2 gap-2 sm:grid-cols-5">
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="form.platform === 'kimi'"
              :aria-pressed="form.platform === 'kimi'"
              title="Kimi"
              :ui="choiceCardUi"
              @click="selectCNPlatform('kimi')"
            >
              <template #leading><PlatformIcon platform="kimi" size="lg" /></template>
            </UPageCard>
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="form.platform === 'zhipu'"
              :aria-pressed="form.platform === 'zhipu'"
              title="Zhipu GLM"
              :ui="choiceCardUi"
              @click="selectCNPlatform('zhipu')"
            >
              <template #leading><PlatformIcon platform="zhipu" size="lg" /></template>
            </UPageCard>
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="form.platform === 'deepseek'"
              :aria-pressed="form.platform === 'deepseek'"
              title="DeepSeek"
              :ui="choiceCardUi"
              @click="selectCNPlatform('deepseek')"
            >
              <template #leading><PlatformIcon platform="deepseek" size="lg" /></template>
            </UPageCard>
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="form.platform === 'minimax'"
              :aria-pressed="form.platform === 'minimax'"
              title="MiniMax"
              :ui="choiceCardUi"
              @click="selectCNPlatform('minimax')"
            >
              <template #leading><PlatformIcon platform="minimax" size="lg" /></template>
            </UPageCard>
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="form.platform === 'typesafe'"
              :aria-pressed="form.platform === 'typesafe'"
              icon="i-lucide-shield-check"
              title="TypeSafe / Jev"
              :ui="choiceCardUi"
              @click="selectTypeSafePlatform()"
            />
          </div>
        </div>

        <!-- Account Type Selection (Anthropic) -->
        <div v-if="form.platform === 'anthropic'">
          <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.accountType') }}</label>
          <div class="mt-2 grid grid-cols-2 gap-3 sm:grid-cols-4" data-tour="account-form-type">
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountCategory === 'oauth-based'"
              :aria-pressed="accountCategory === 'oauth-based'"
              icon="i-lucide-sparkles"
              :title="t('admin.accounts.claudeCode')"
              :description="t('admin.accounts.oauthSetupToken')"
              :ui="choiceCardUi"
              @click="accountCategory = 'oauth-based'"
            />
  
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountCategory === 'apikey'"
              :aria-pressed="accountCategory === 'apikey'"
              icon="i-lucide-key"
              :title="t('admin.accounts.claudeConsole')"
              :description="t('admin.accounts.apiKey')"
              :ui="choiceCardUi"
              @click="accountCategory = 'apikey'"
            />
  
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountCategory === 'bedrock'"
              :aria-pressed="accountCategory === 'bedrock'"
              icon="i-lucide-cloud"
              :title="t('admin.accounts.bedrockLabel')"
              :description="t('admin.accounts.bedrockDesc')"
              :ui="choiceCardUi"
              @click="accountCategory = 'bedrock'"
            />
  
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountCategory === 'service_account'"
              :aria-pressed="accountCategory === 'service_account'"
              icon="i-lucide-cloud"
              :title="'Vertex'"
              :description="'Service Account'"
              :ui="choiceCardUi"
              @click="accountCategory = 'service_account'"
            />
  
          </div>
  
          <UAlert v-if="accountCategory === 'service_account'" color="neutral" variant="subtle" icon="i-lucide-info" :description="t('admin.accounts.vertexAnthropicHint')" class="mt-3" />
        </div>

        <!-- Account Type Selection (OpenAI) -->
        <div v-if="form.platform === 'openai'">
          <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.accountType') }}</label>
          <div class="mt-2 grid grid-cols-1 gap-3 sm:grid-cols-3" data-tour="account-form-type">
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountCategory === 'oauth-based'"
              :aria-pressed="accountCategory === 'oauth-based'"
              icon="i-lucide-key"
              :title="'OAuth'"
              :description="t('admin.accounts.types.chatgptOauth')"
              :ui="choiceCardUi"
              @click="accountCategory = 'oauth-based'"
            />
  
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountCategory === 'apikey'"
              :aria-pressed="accountCategory === 'apikey'"
              icon="i-lucide-key"
              :title="'API Key'"
              :description="t('admin.accounts.types.responsesApi')"
              :ui="choiceCardUi"
              @click="accountCategory = 'apikey'"
            />
  
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountCategory === 'cloudflare'"
              :aria-pressed="accountCategory === 'cloudflare'"
              icon="i-lucide-cloud"
              :title="'Cloudflare'"
              :description="t('admin.accounts.types.cloudflareWorkers')"
              :ui="choiceCardUi"
              @click="accountCategory = 'cloudflare'"
            />
  
          </div>
        </div>

        <!-- Account Type Selection (Grok) -->
        <div v-if="form.platform === 'grok'">
          <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.accountType') }}</label>
          <div class="mt-2 grid grid-cols-1 gap-3 sm:grid-cols-2" data-tour="account-form-type">
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountCategory === 'oauth-based'"
              :aria-pressed="accountCategory === 'oauth-based'"
              :title="'OAuth'"
              :description="t('admin.accounts.types.grokOauth')"
              :ui="choiceCardUi"
              @click="accountCategory = 'oauth-based'"
            >
              <template #leading><PlatformIcon platform="grok" size="lg" /></template>
            </UPageCard>
  
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountCategory === 'apikey'"
              :aria-pressed="accountCategory === 'apikey'"
              icon="i-lucide-key"
              :title="'API Key'"
              :description="t('admin.accounts.types.responsesApi')"
              :ui="choiceCardUi"
              data-testid="grok-account-type-api-key"
              @click="accountCategory = 'apikey'"
            />
          </div>
        </div>

        <!-- Account Mode Selection (Kimi / Zhipu / DeepSeek) -->
        <div v-if="isCNPlatform">
          <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.cnProviders.accountMode.title') }}</label>
          <div class="mt-2 grid grid-cols-1 gap-3 sm:grid-cols-2" data-tour="account-form-mode">
            <!-- Pay-as-you-go (token balance) -->
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountMode === 'payg'"
              :aria-pressed="accountMode === 'payg'"
              icon="i-lucide-credit-card"
              :title="t('admin.accounts.cnProviders.accountMode.payg')"
              :description="t('admin.accounts.cnProviders.accountMode.paygDesc')"
              :ui="choiceCardUi"
              @click="accountMode = 'payg'"
            />
            <!-- Coding Plan (kimi / zhipu only — DeepSeek has no coding plan) -->
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountMode === 'coding'"
              :aria-pressed="accountMode === 'coding'"
              icon="i-lucide-zap"
              :title="t('admin.accounts.cnProviders.accountMode.coding')"
              :description="t('admin.accounts.cnProviders.accountMode.codingDesc')"
              :ui="choiceCardUi"
              v-if="form.platform !== 'deepseek'"
              @click="accountMode = 'coding'"
            />
          </div>
        </div>

        <!-- API Protocol Selection (Kimi / Zhipu / DeepSeek) -->
        <div v-if="isAdaptiveProtocolAccount" class="mt-4">
          <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.cnProviders.apiProtocol.title') }}</label>
          <div class="mt-2 grid grid-cols-1 gap-3 sm:grid-cols-3">
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="apiProtocol === opt.value"
              :aria-pressed="apiProtocol === opt.value"
              :title="t(`admin.accounts.cnProviders.apiProtocol.${opt.labelKey}`)"
              :description="t(`admin.accounts.cnProviders.apiProtocol.${opt.labelKey}Desc`)"
              :ui="choiceCardUi"
              v-for="opt in cnProtocolOptions"
              :key="opt.value"
              @click="apiProtocol = opt.value"
            >
              <template #leading><Icon :name="opt.value === 'adaptive' ? 'swap' : opt.value === 'anthropic' ? 'sparkles' : opt.value === 'responses' ? 'terminal' : 'chat'" size="sm" /></template>
            </UPageCard>
          </div>
        </div>

        <!-- Account Type Selection (Gemini) -->
        <div v-if="form.platform === 'gemini'">
          <div class="flex items-center justify-between">
            <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.accountType') }}</label>
            <UButton
              color="primary"
              variant="link"
              size="xs"
              icon="i-lucide-circle-help"
              type="button"
              @click="showGeminiHelpDialog = true"
            >{{ t('admin.accounts.gemini.helpButton') }}</UButton>
          </div>
          <div class="mt-2 grid grid-cols-2 gap-3 lg:grid-cols-3" data-tour="account-form-type">
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountCategory === 'oauth-based'"
              :aria-pressed="accountCategory === 'oauth-based'"
              icon="i-lucide-key"
              :title="t('admin.accounts.gemini.accountType.oauthTitle')"
              :description="t('admin.accounts.gemini.accountType.oauthDesc')"
              :ui="choiceCardUi"
              @click="accountCategory = 'oauth-based'"
            />
  
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountCategory === 'apikey'"
              :aria-pressed="accountCategory === 'apikey'"
              icon="i-lucide-key-round"
              :title="t('admin.accounts.gemini.accountType.apiKeyTitle')"
              :description="t('admin.accounts.gemini.accountType.apiKeyDesc')"
              :ui="choiceCardUi"
              @click="accountCategory = 'apikey'"
            />
  
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="accountCategory === 'service_account'"
              :aria-pressed="accountCategory === 'service_account'"
              icon="i-lucide-cloud"
              :title="'Vertex'"
              :description="'Service Account'"
              :ui="choiceCardUi"
              @click="accountCategory = 'service_account'"
            />
          </div>
  
          <div
            v-if="accountCategory === 'apikey'"
            class="mt-3 text-xs text-muted"
          >
            <p>{{ t('admin.accounts.gemini.accountType.apiKeyNote') }}</p>
            <div class="mt-2 flex flex-wrap gap-2">
              <a
                :href="geminiHelpLinks.apiKey"
                class="font-medium text-primary hover:underline"
                target="_blank"
                rel="noreferrer"
              >
                {{ t('admin.accounts.gemini.accountType.apiKeyLink') }}
              </a>
            </div>
          </div>
  
          <UAlert v-if="accountCategory === 'service_account'" color="neutral" variant="subtle" icon="i-lucide-info" :description="t('admin.accounts.vertexGeminiHint')" class="mt-3" />
  
          <!-- OAuth Type Selection (only show when oauth-based is selected) -->
          <div v-if="accountCategory === 'oauth-based'" class="mt-4">
            <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.oauth.gemini.oauthTypeLabel') }}</label>
            <div class="mt-2 grid grid-cols-2 gap-3">
              <!-- Google One OAuth -->
              <UPageCard
                as="button"
                type="button"
                variant="outline"
                :highlight="geminiOAuthType === 'google_one'"
                :aria-pressed="geminiOAuthType === 'google_one'"
                icon="i-lucide-user"
                :title="'Google One'"
                :description="t('admin.accounts.gemini.oauthType.googleOneDesc')"
                :ui="choiceCardUi"
                @click="handleSelectGeminiOAuthType('google_one')"
              >
                <template #default><div class="ms-8 mt-2"><div class="mt-2 flex flex-wrap gap-1">
                    <UBadge color="neutral" variant="subtle" size="sm">
                      {{ t('admin.accounts.gemini.oauthType.badges.individuals') }}
                    </UBadge>
                    <UBadge color="neutral" variant="subtle" size="sm">
                      {{ t('admin.accounts.gemini.oauthType.badges.noGcp') }}
                    </UBadge>
                  </div></div></template>
              </UPageCard>
  
              <!-- GCP Code Assist OAuth -->
              <UPageCard
                as="button"
                type="button"
                variant="outline"
                :highlight="geminiOAuthType === 'code_assist'"
                :aria-pressed="geminiOAuthType === 'code_assist'"
                icon="i-lucide-cloud"
                :title="'GCP Code Assist'"
                :description="t('admin.accounts.gemini.oauthType.codeAssistDesc')"
                :ui="choiceCardUi"
                @click="handleSelectGeminiOAuthType('code_assist')"
              >
                <template #default><div class="ms-8 mt-2"><div class="mt-1 text-xs text-muted">
                    {{ t('admin.accounts.gemini.oauthType.codeAssistRequirement') }}
                    <a
                      :href="geminiHelpLinks.gcpProject"
                      class="ml-1 text-primary hover:underline"
                      target="_blank"
                      rel="noreferrer"
                    >
                      {{ t('admin.accounts.gemini.oauthType.gcpProjectLink') }}
                    </a>
                  </div>
  <div class="mt-2 flex flex-wrap gap-1">
                    <UBadge color="neutral" variant="subtle" size="sm">
                      {{ t('admin.accounts.gemini.oauthType.badges.enterprise') }}
                    </UBadge>
                    <UBadge color="neutral" variant="subtle" size="sm">
                      {{ t('admin.accounts.gemini.oauthType.badges.highConcurrency') }}
                    </UBadge>
                  </div></div></template>
              </UPageCard>
            </div>
  
            <!-- Advanced Options Toggle -->
            <div class="mt-3">
              <UButton
                color="neutral"
                variant="link"
                size="sm"
                class="px-0"
                type="button"
                @click="showAdvancedOAuth = !showAdvancedOAuth"
              >
                <UIcon name="i-lucide-chevron-right" :class="['size-4 transition-transform', showAdvancedOAuth ? 'rotate-90' : '']" />
                <span>
                  {{
                    showAdvancedOAuth
                      ? t('admin.accounts.gemini.oauthType.hideAdvanced')
                      : t('admin.accounts.gemini.oauthType.showAdvanced')
                  }}
                </span>
              </UButton>
            </div>
  
            <!-- Custom OAuth Client (Advanced) -->
            <div v-if="showAdvancedOAuth" class="mt-3 group relative">
              <UPageCard
                as="button"
                type="button"
                variant="outline"
                :highlight="geminiOAuthType === 'ai_studio'"
                :aria-pressed="geminiOAuthType === 'ai_studio'"
                icon="i-lucide-sparkles"
                :title="t('admin.accounts.gemini.oauthType.customTitle')"
                :description="t('admin.accounts.gemini.oauthType.customDesc')"
                :ui="choiceCardUi"
                class="disabled:cursor-not-allowed disabled:opacity-60"
                :disabled="!geminiAIStudioOAuthEnabled"
                @click="handleSelectGeminiOAuthType('ai_studio')"
              >
                <template #default><div class="ms-8 mt-2"><div class="mt-1 text-xs text-muted">
                    {{ t('admin.accounts.gemini.oauthType.customRequirement') }}
                  </div>
  <div class="mt-2 flex flex-wrap gap-1">
                    <UBadge color="neutral" variant="subtle" size="sm">
                      {{ t('admin.accounts.gemini.oauthType.badges.orgManaged') }}
                    </UBadge>
                    <UBadge color="neutral" variant="subtle" size="sm">
                      {{ t('admin.accounts.gemini.oauthType.badges.adminRequired') }}
                    </UBadge>
                  </div>
  <span
                  v-if="!geminiAIStudioOAuthEnabled"
                  class="ml-auto shrink-0 rounded-sm bg-warning/10 px-2 py-0.5 text-xs text-warning"
                >
                  {{ t('admin.accounts.oauth.gemini.aiStudioNotConfiguredShort') }}
                </span></div></template>
              </UPageCard>
  
              <div
                v-if="!geminiAIStudioOAuthEnabled"
                class="pointer-events-none absolute right-0 top-full z-50 mt-2 w-80 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-xs text-warning opacity-0 shadow-lg transition-opacity group-hover:opacity-100"
              >
                {{ t('admin.accounts.oauth.gemini.aiStudioNotConfiguredTip') }}
              </div>
            </div>
          </div>
  
          <!-- Tier selection (used as fallback when auto-detection is unavailable/fails) -->
          <div v-if="accountCategory !== 'service_account'" class="mt-4">
            <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.gemini.tier.label') }}</label>
            <div class="mt-2">
              <Select
                v-if="geminiOAuthType === 'google_one'"
                v-model="geminiTierGoogleOne"
                :options="[
                  { value: 'google_one_free', label: t('admin.accounts.gemini.tier.googleOne.free') },
                  { value: 'google_ai_pro', label: t('admin.accounts.gemini.tier.googleOne.pro') },
                  { value: 'google_ai_ultra', label: t('admin.accounts.gemini.tier.googleOne.ultra') }
                ]"
              />
  
              <Select
                v-else-if="geminiOAuthType === 'code_assist'"
                v-model="geminiTierGcp"
                :options="[
                  { value: 'gcp_standard', label: t('admin.accounts.gemini.tier.gcp.standard') },
                  { value: 'gcp_enterprise', label: t('admin.accounts.gemini.tier.gcp.enterprise') }
                ]"
              />
  
              <Select
                v-else
                v-model="geminiTierAIStudio"
                :options="[
                  { value: 'aistudio_free', label: t('admin.accounts.gemini.tier.aiStudio.free') },
                  { value: 'aistudio_paid', label: t('admin.accounts.gemini.tier.aiStudio.paid') }
                ]"
              />
            </div>
            <p class="mt-1.5 text-xs text-muted">{{ t('admin.accounts.gemini.tier.hint') }}</p>
          </div>
        </div>

        <!-- Account Type Selection (Antigravity - OAuth or Upstream) -->
        <div v-if="form.platform === 'antigravity'">
          <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.accountType') }}</label>
          <div class="mt-2 grid grid-cols-2 gap-3">
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="antigravityAccountType === 'oauth'"
              :aria-pressed="antigravityAccountType === 'oauth'"
              icon="i-lucide-key"
              :title="'OAuth'"
              :description="t('admin.accounts.types.antigravityOauth')"
              :ui="choiceCardUi"
              @click="antigravityAccountType = 'oauth'"
            />
  
            <UPageCard
              as="button"
              type="button"
              variant="outline"
              :highlight="antigravityAccountType === 'upstream'"
              :aria-pressed="antigravityAccountType === 'upstream'"
              icon="i-lucide-cloud"
              :title="'API Key'"
              :description="t('admin.accounts.types.antigravityApikey')"
              :ui="choiceCardUi"
              @click="antigravityAccountType = 'upstream'"
            />
          </div>
        </div>

        <!-- Add Method (only for Anthropic OAuth-based type) -->
        <UFormField v-if="form.platform === 'anthropic' && isOAuthFlow" :label="t('admin.accounts.addMethod')">
          <URadioGroup
            v-model="addMethod"
            :items="[
              { label: t('admin.accounts.types.oauth'), value: 'oauth' },
              { label: t('admin.accounts.setupTokenLongLived'), value: 'setup-token' }
            ]"
            orientation="horizontal"
            :ui="{ fieldset: 'flex-wrap gap-x-4 gap-y-2' }"
            class="mt-2"
          />
        </UFormField>

        <h4 class="text-sm font-semibold text-highlighted">{{ t('admin.accounts.formSections.basic') }}</h4>

        <UFormField :label="t('admin.accounts.accountName')">
          <UInput
            v-model="form.name"
            type="text"
            :required="!isGrokSSOInputMethod"
            :placeholder="t('admin.accounts.enterAccountName')"
            data-tour="account-form-name"
            class="w-full"
          />
        </UFormField>

        <UFormField :label="t('admin.accounts.notes')" :help="t('admin.accounts.notesHint')">
          <UTextarea
            :rows="3"
            v-model="form.notes"
            :placeholder="t('admin.accounts.notesPlaceholder')"
            class="w-full"
          ></UTextarea>
        </UFormField>
      </div>
      <div v-show="showWizardStep(1)" data-wizard-step="1" class="space-y-6">
        <UFormField v-if="form.platform === 'antigravity' && antigravityAccountType === 'oauth'" :label="t('admin.accounts.antigravityProjectIdLabel')" :help="t('admin.accounts.antigravityProjectIdHint')">
          <UInput
            v-model="antigravityProjectId"
            data-testid="antigravity-project-id-input"
            type="text"
            :placeholder="t('admin.accounts.antigravityProjectIdPlaceholder')"
            class="w-full font-mono"
          />
        </UFormField>

        <!-- Zhipu 团队版 Coding Plan：组织/项目 ID（可选，填写后额度探测走团队版端点） -->
        <div v-if="form.platform === 'zhipu' && accountMode === 'coding'" class="mt-4">
          <div class="flex items-center">
            <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.cnProviders.zhipuTeam.title') }}</label>
            <HelpTooltip trigger="click" width-class="w-80">
              <p class="mb-1 font-medium">{{ t('admin.accounts.cnProviders.zhipuTeam.help.title') }}</p>
              <ol class="list-decimal space-y-1 pl-4">
                <li>{{ t('admin.accounts.cnProviders.zhipuTeam.help.step1') }}</li>
                <li>{{ t('admin.accounts.cnProviders.zhipuTeam.help.step2') }}</li>
                <li>{{ t('admin.accounts.cnProviders.zhipuTeam.help.step3') }}</li>
                <li>{{ t('admin.accounts.cnProviders.zhipuTeam.help.step4') }}</li>
              </ol>
              <p class="mt-2 break-all rounded-sm bg-inverted p-1.5 font-mono text-[11px] leading-relaxed">
                {{ t('admin.accounts.cnProviders.zhipuTeam.help.example') }}
              </p>
            </HelpTooltip>
          </div>
          <div class="mt-2 grid gap-4 sm:grid-cols-2">
            <UFormField :label="t('admin.accounts.cnProviders.zhipuTeam.organization')">
              <UInput v-model="zhipuOrganization" type="text" :placeholder="t('admin.accounts.cnProviders.zhipuTeam.organizationPlaceholder')" class="w-full" />
            </UFormField>
            <UFormField :label="t('admin.accounts.cnProviders.zhipuTeam.project')">
              <UInput v-model="zhipuProject" type="text" :placeholder="t('admin.accounts.cnProviders.zhipuTeam.projectPlaceholder')" class="w-full" />
            </UFormField>
          </div>
          <p class="mt-1.5 text-xs text-muted mt-2">{{ t('admin.accounts.cnProviders.zhipuTeam.hint') }}</p>
        </div>

        <!-- Upstream config (only for Antigravity upstream type) -->
        <div v-if="form.platform === 'antigravity' && antigravityAccountType === 'upstream'" class="space-y-4">
          <UFormField :label="t('admin.accounts.upstream.baseUrl')" :help="t('admin.accounts.upstream.baseUrlHint')">
            <UInput
              v-model="upstreamBaseUrl"
              type="text"
              required
              placeholder="https://cloudcode-pa.googleapis.com"
              class="w-full"
            />
          </UFormField>
          <UFormField :label="t('admin.accounts.upstream.apiKey')" :help="t('admin.accounts.upstream.apiKeyHint')">
            <UInput
              v-model="upstreamApiKey"
              type="password"
              required
              placeholder="sk-..."
              class="w-full font-mono"
            />
          </UFormField>
          <!-- 上游倍率自动探测：antigravity upstream 也是 API-key 账号 -->
          <div class="flex items-center justify-between gap-4 pt-2">
            <div>
              <label class="block text-sm font-medium text-default">{{ t('admin.accounts.upstreamBilling.autoProbe') }}</label>
              <p class="mt-1 text-xs text-muted">
                {{ t('admin.accounts.upstreamBilling.autoProbeHint') }}
              </p>
            </div>
            <Toggle
              v-model="upstreamBillingAutoProbeEnabled"
              data-testid="upstream-billing-auto-probe-antigravity"
              :aria-label="t('admin.accounts.upstreamBilling.autoProbe')"
            />
          </div>
        </div>

        <!-- Vertex Service Account -->
        <div v-if="(form.platform === 'gemini' || form.platform === 'anthropic') && accountCategory === 'service_account'" class="space-y-4">
          <div>
            <label class="mb-1.5 block text-sm font-medium text-default">Service Account JSON</label>
            <input
              ref="vertexServiceAccountFileInput"
              type="file"
              accept="application/json,.json"
              class="hidden"
              @change="handleVertexServiceAccountFile"
            />
            <div
              :class="[
                'rounded-lg border-2 border-dashed px-4 py-5 transition-colors',
                vertexServiceAccountDragActive
                  ? 'border-primary bg-elevated'
                  : 'border-accented bg-elevated'
              ]"
              @dragenter.prevent="vertexServiceAccountDragActive = true"
              @dragover.prevent="vertexServiceAccountDragActive = true"
              @dragleave.prevent="vertexServiceAccountDragActive = false"
              @drop.prevent="handleVertexServiceAccountDrop"
            >
              <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                <div class="min-w-0">
                  <div class="flex items-center gap-2 text-sm font-medium text-highlighted">
                    <Icon name="upload" size="sm" />
                    <span>{{ vertexClientEmail ? t('admin.accounts.vertexSaJsonLoaded') : t('admin.accounts.vertexSaJsonDrop') }}</span>
                  </div>
                  <p class="mt-1 text-xs text-muted">
                    {{ vertexClientEmail ? t('admin.accounts.vertexSaJsonKeyHidden') : t('admin.accounts.vertexSaJsonDropHint') }}
                  </p>
                </div>
                <UButton
                  color="neutral"
                  variant="outline"
                  type="button"
                  @click="vertexServiceAccountFileInput?.click()"
                  class="shrink-0"
                >
                  <Icon name="upload" size="sm" />
                  {{ t('admin.accounts.vertexSaJsonSelectBtn') }}
                </UButton>
              </div>
              <div
                v-if="vertexClientEmail"
                class="mt-3 rounded-md border border-default bg-default px-3 py-2 text-xs text-highlighted"
              >
                <div class="truncate">Project ID: <span class="font-mono">{{ vertexProjectId }}</span></div>
                <div class="truncate">Client Email: <span class="font-mono">{{ vertexClientEmail }}</span></div>
              </div>
            </div>
            <p class="mt-1.5 text-xs text-muted">{{ t('admin.accounts.vertexSaJsonUploadHint') }}</p>
          </div>
  
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <UFormField :label="'Project ID'">
              <UInput
                v-model="vertexProjectId"
                type="text"
                readonly
                :placeholder="t('admin.accounts.vertexProjectIdPlaceholder')"
                class="w-full font-mono"
              />
            </UFormField>
            <UFormField :label="'Location'" :help="t('admin.accounts.vertexLocationHint')">
              <Select
                v-model="vertexLocation"
                required
                :options="[
                  ...VERTEX_LOCATION_OPTIONS.flatMap((group) => [{ value: `__group:${group.label}`, label: group.label, kind: 'group', disabled: true }, ...group.options.map((option) => ({ value: option.value, label: option.label }))])
                ]"
              />
            </UFormField>
          </div>
        </div>

        <!-- API Key input (only for apikey type, excluding Antigravity which has its own fields) -->
        <div v-if="(form.type === 'apikey' || form.type === 'cloudflare') && form.platform !== 'antigravity'" class="space-y-4">
          <UFormField v-if="form.type === 'cloudflare'" :label="t('admin.accounts.openai.cloudflareAccountId')" :help="t('admin.accounts.openai.cloudflareAccountIdHint')">
            <UInput
              v-model="cloudflareAccountID"
              type="text"
              required
              :placeholder="t('admin.accounts.openai.cloudflareAccountIdPlaceholder')"
              class="w-full font-mono"
            />
          </UFormField>
          <div v-else-if="!isAdaptiveProtocolAccount || apiProtocol !== 'adaptive'">
            <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.baseUrl') }}</label>
            <UInput
              v-model="apiKeyBaseUrl"
              type="text"
              :placeholder="apiKeyBaseUrlPlaceholder"
              class="w-full"
            />
            <p v-if="baseUrlHint" class="mt-1.5 text-xs text-muted">{{ baseUrlHint }}</p>
            <GrokBaseUrlPresets
              v-if="form.platform === 'grok'"
              class="mt-2"
              @select="apiKeyBaseUrl = $event"
            />
            <CnBaseUrlPresets
              v-if="isCNPlatform"
              class="mt-2"
              :platform="cnPresetPlatform"
              :mode="accountMode"
              :protocol="apiProtocol"
              :current-url="apiKeyBaseUrl"
              @select="onCnPresetSelect"
            />
          </div>
          <div v-else>
            <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.cnProviders.apiProtocol.endpoints') }}</label>
            <div class="mt-2 space-y-3">
              <div v-for="item in adaptiveProtocolEndpointOptions" :key="item.value">
                <label class="mb-1 block text-xs font-medium text-toned">
                  {{ t(`admin.accounts.cnProviders.apiProtocol.${item.labelKey}`) }}
                </label>
                <UInput
                  v-model="adaptiveBaseUrls[item.value]"
                  type="text"
                  :data-testid="`cn-adaptive-base-url-${item.value}`"
                  class="w-full"
                />
              </div>
            </div>
            <p v-if="!cnSupportsNativeResponses(form.platform)" class="mt-1.5 text-xs text-muted">
              {{ t('admin.accounts.cnProviders.apiProtocol.responsesFallbackDesc') }}
            </p>
          </div>
  
          <UFormField :label="t('admin.accounts.apiKeyRequired')">
            <UInput
              v-model="apiKeyValue"
              type="password"
              required
              :placeholder="apiKeyValuePlaceholder"
              class="w-full font-mono"
            />
            <template #help><span v-if="apiKeyHint" class="block">{{ apiKeyHint }}</span><span v-if="form.type === 'cloudflare'" class="block">{{ t('admin.accounts.openai.cloudflareApiTokenHint') }}</span></template>
          </UFormField>
  
          <!-- 上游倍率自动探测：Cloudflare 没有这套探测接口 -->
          <div
            v-if="form.type !== 'cloudflare'"
            class="flex items-center justify-between gap-4 pt-2"
          >
            <div>
              <label class="block text-sm font-medium text-default">{{ t('admin.accounts.upstreamBilling.autoProbe') }}</label>
              <p class="mt-1 text-xs text-muted">
                {{ t('admin.accounts.upstreamBilling.autoProbeHint') }}
              </p>
            </div>
            <Toggle
              v-model="upstreamBillingAutoProbeEnabled"
              data-testid="upstream-billing-auto-probe"
              :aria-label="t('admin.accounts.upstreamBilling.autoProbe')"
            />
          </div>
  
          <div v-if="form.platform === 'gemini' && accountCategory === 'apikey'">
            <UCheckbox v-model="geminiUseCustomProtocol" data-testid="gemini-custom-protocol" :label="t('admin.accounts.gemini.accountType.customProtocolTitle')" :description="t('admin.accounts.gemini.accountType.customProtocolDesc')" />
          </div>
  
          <CopilotSDKFields v-if="form.platform === 'openai' && accountCategory === 'apikey'" v-model="copilotSDKEnabled" />
  
          <GeminiCacheAccountFields
            v-if="form.platform === 'gemini' && accountCategory === 'apikey' && !geminiUseCustomProtocol"
            v-model="geminiCacheSettings"
          />
  
          <!-- Gemini API Key tier selection -->
          <UFormField v-if="form.platform === 'gemini' && accountCategory === 'apikey' && !geminiUseCustomProtocol" :label="t('admin.accounts.gemini.tier.label')" :help="t('admin.accounts.gemini.tier.aiStudioHint')">
            <Select
              v-model="geminiTierAIStudio"
              :options="[
                { value: 'aistudio_free', label: t('admin.accounts.gemini.tier.aiStudio.free') },
                { value: 'aistudio_paid', label: t('admin.accounts.gemini.tier.aiStudio.paid') }
              ]"
            />
          </UFormField>
        </div>

        <!-- Bedrock credentials (only for Anthropic Bedrock type) -->
        <div v-if="form.platform === 'anthropic' && accountCategory === 'bedrock'" class="space-y-4">
          <!-- Auth Mode Radio -->
          <UFormField :label="t('admin.accounts.bedrockAuthMode')">
            <URadioGroup
              v-model="bedrockAuthMode"
              :items="[
                { label: t('admin.accounts.bedrockAuthModeSigv4'), value: 'sigv4' },
                { label: t('admin.accounts.bedrockAuthModeApikey'), value: 'apikey' }
              ]"
              orientation="horizontal"
              :ui="{ fieldset: 'flex-wrap gap-x-4 gap-y-2' }"
              class="mt-2"
            />
          </UFormField>
  
          <!-- SigV4 fields -->
          <template v-if="bedrockAuthMode === 'sigv4'">
            <UFormField :label="t('admin.accounts.bedrockAccessKeyId')">
              <UInput
                v-model="bedrockAccessKeyId"
                type="text"
                required
                placeholder="AKIA..."
                class="w-full font-mono"
              />
            </UFormField>
            <UFormField :label="t('admin.accounts.bedrockSecretAccessKey')">
              <UInput
                v-model="bedrockSecretAccessKey"
                type="password"
                required
                class="w-full font-mono"
              />
            </UFormField>
            <UFormField :label="t('admin.accounts.bedrockSessionToken')" :help="t('admin.accounts.bedrockSessionTokenHint')">
              <UInput
                v-model="bedrockSessionToken"
                type="password"
                class="w-full font-mono"
              />
            </UFormField>
          </template>
  
          <!-- API Key field -->
          <UFormField v-if="bedrockAuthMode === 'apikey'" :label="t('admin.accounts.bedrockApiKeyInput')">
            <UInput
              v-model="bedrockApiKeyValue"
              type="password"
              required
              class="w-full font-mono"
            />
          </UFormField>
  
          <!-- Shared: Region -->
          <UFormField :label="t('admin.accounts.bedrockRegion')" :help="t('admin.accounts.bedrockRegionHint')">
            <Select
              v-model="bedrockRegion"
              :options="[
                { value: `__group:${'US'}`, label: 'US', kind: 'group', disabled: true },
                { value: 'us-east-1', label: 'us-east-1 (N. Virginia)' },
                { value: 'us-east-2', label: 'us-east-2 (Ohio)' },
                { value: 'us-west-1', label: 'us-west-1 (N. California)' },
                { value: 'us-west-2', label: 'us-west-2 (Oregon)' },
                { value: 'us-gov-east-1', label: 'us-gov-east-1 (GovCloud US-East)' },
                { value: 'us-gov-west-1', label: 'us-gov-west-1 (GovCloud US-West)' },
                { value: `__group:${'Europe'}`, label: 'Europe', kind: 'group', disabled: true },
                { value: 'eu-west-1', label: 'eu-west-1 (Ireland)' },
                { value: 'eu-west-2', label: 'eu-west-2 (London)' },
                { value: 'eu-west-3', label: 'eu-west-3 (Paris)' },
                { value: 'eu-central-1', label: 'eu-central-1 (Frankfurt)' },
                { value: 'eu-central-2', label: 'eu-central-2 (Zurich)' },
                { value: 'eu-south-1', label: 'eu-south-1 (Milan)' },
                { value: 'eu-south-2', label: 'eu-south-2 (Spain)' },
                { value: 'eu-north-1', label: 'eu-north-1 (Stockholm)' },
                { value: `__group:${'Asia Pacific'}`, label: 'Asia Pacific', kind: 'group', disabled: true },
                { value: 'ap-northeast-1', label: 'ap-northeast-1 (Tokyo)' },
                { value: 'ap-northeast-2', label: 'ap-northeast-2 (Seoul)' },
                { value: 'ap-northeast-3', label: 'ap-northeast-3 (Osaka)' },
                { value: 'ap-south-1', label: 'ap-south-1 (Mumbai)' },
                { value: 'ap-south-2', label: 'ap-south-2 (Hyderabad)' },
                { value: 'ap-southeast-1', label: 'ap-southeast-1 (Singapore)' },
                { value: 'ap-southeast-2', label: 'ap-southeast-2 (Sydney)' },
                { value: `__group:${'Canada'}`, label: 'Canada', kind: 'group', disabled: true },
                { value: 'ca-central-1', label: 'ca-central-1 (Canada)' },
                { value: `__group:${'South America'}`, label: 'South America', kind: 'group', disabled: true },
                { value: 'sa-east-1', label: 'sa-east-1 (São Paulo)' }
              ]"
            />
          </UFormField>
  
          <!-- Shared: Force Global -->
          <div>
            <UCheckbox v-model="bedrockForceGlobal" :label="t('admin.accounts.bedrockForceGlobal')" />
            <p class="mt-1.5 text-xs text-muted mt-1">{{ t('admin.accounts.bedrockForceGlobalHint') }}</p>
          </div>
        </div>

        <!-- Grok OAuth Custom Upstream URL (仅改写转发端点，OAuth 授权/刷新不受影响) -->
        <div
          v-if="form.platform === 'grok' && isOAuthFlow"
          class="pt-2"
        >
          <UFormField :label="t('admin.accounts.grokCustomBaseUrl.title')" :description="t('admin.accounts.grokCustomBaseUrl.hint')" class="flex items-center justify-between gap-4 mb-3">
            <Toggle v-model="grokOAuthCustomBaseUrlEnabled" data-testid="grok-custom-base-url-toggle" />
          </UFormField>
          <div v-if="grokOAuthCustomBaseUrlEnabled" class="space-y-2">
            <UInput
              v-model="grokOAuthBaseUrl"
              type="text"
              data-testid="grok-custom-base-url-input"
              :placeholder="t('admin.accounts.grokCustomBaseUrl.placeholder')"
              class="w-full"
            />
            <GrokBaseUrlPresets @select="grokOAuthBaseUrl = $event" />
          </div>
        </div>
      </div>
      <div v-show="showWizardStep(2)" data-wizard-step="2" class="space-y-6">

        <!-- Antigravity model restriction (applies to OAuth + Upstream) -->
        <!-- Antigravity 只支持模型映射模式，不支持白名单模式 -->
        <div v-if="form.platform === 'antigravity'" class="pt-2">
          <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.modelRestriction') }}</label>
  
          <!-- Mapping Mode Only (no toggle for Antigravity) -->
          <div>
            <UAlert color="neutral" variant="subtle" icon="i-lucide-info" :description="t('admin.accounts.mapRequestModels')" class="mb-3" />
            <ModelWhitelistSelector
              sync-only
              :model-value="antigravityMappingFromKeys"
              platform="antigravity"
              :sync-credentials="syncPreviewCredentials"
              @upstream-models="importUpstreamModelsToAntigravityMapping"
            />
  
            <div v-if="antigravityModelMappings.length > 0" class="mb-3 space-y-2">
              <div
                v-for="(mapping, index) in antigravityModelMappings"
                :key="getAntigravityModelMappingKey(mapping)"
                class="space-y-1"
              >
                <div class="flex items-center gap-2">
                  <UInput v-model="mapping.from" type="text" :placeholder="t('admin.accounts.requestModel')" :color="!isValidWildcardPattern(mapping.from) ? 'error' : undefined" :highlight="!isValidWildcardPattern(mapping.from)" class="flex-1" />
                  <UIcon name="i-lucide-arrow-right" class="size-4 flex-shrink-0 text-muted" />
                  <UInput v-model="mapping.to" type="text" :placeholder="t('admin.accounts.actualModel')" :color="mapping.to.includes('*') ? 'error' : undefined" :highlight="mapping.to.includes('*')" class="flex-1" />
                  <UButton
                    color="error"
                    variant="ghost"
                    square
                    size="sm"
                    icon="i-lucide-trash-2"
                    :aria-label="t('admin.accounts.formActions.remove')"
                    type="button"
                    @click="removeAntigravityModelMapping(index)"
                  />
                </div>
                <!-- 校验错误提示 -->
                <p v-if="!isValidWildcardPattern(mapping.from)" class="text-xs text-error">
                  {{ t('admin.accounts.wildcardOnlyAtEnd') }}
                </p>
                <p v-if="mapping.to.includes('*')" class="text-xs text-error">
                  {{ t('admin.accounts.targetNoWildcard') }}
                </p>
              </div>
            </div>
  
            <UButton
              color="neutral"
              variant="soft"
              block
              icon="i-lucide-plus"
              class="mb-3"
              type="button"
              @click="addAntigravityModelMapping"
            >{{ t('admin.accounts.addMapping') }}</UButton>
  
            <div class="flex flex-wrap gap-2">
              <UButton
                color="neutral"
                variant="soft"
                size="xs"
                v-for="preset in antigravityPresetMappings"
                :key="preset.label"
                type="button"
                @click="addAntigravityPresetMapping(preset.from, preset.to)"
              >
                + {{ preset.label }}
              </UButton>
            </div>
          </div>
        </div>

        <!-- OpenAI OAuth Model Mapping (OAuth 类型没有 apikey 容器，需要独立的模型映射区域) -->
        <div
          v-if="(form.platform === 'openai' || form.platform === 'grok') && isOAuthFlow"
          class="pt-2"
        >
          <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.modelRestriction') }}</label>
  
          <UAlert v-if="isOpenAIModelRestrictionDisabled" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.accounts.openai.modelRestrictionDisabledByPassthrough')" class="mb-3" />
  
          <template v-else>
            <!-- Mode Toggle -->
            <div class="mb-4 flex gap-1 rounded-lg bg-elevated p-1">
              <UButton
                :color="modelRestrictionMode === 'whitelist' ? 'primary' : 'neutral'"
                :variant="modelRestrictionMode === 'whitelist' ? 'solid' : 'ghost'"
                size="sm"
                block
                class="flex-1"
                type="button"
                @click="modelRestrictionMode = 'whitelist'"
              >{{ t('admin.accounts.modelWhitelist') }}</UButton>
              <UButton
                :color="modelRestrictionMode === 'mapping' ? 'primary' : 'neutral'"
                :variant="modelRestrictionMode === 'mapping' ? 'solid' : 'ghost'"
                size="sm"
                block
                class="flex-1"
                type="button"
                @click="modelRestrictionMode = 'mapping'"
              >{{ t('admin.accounts.modelMapping') }}</UButton>
            </div>
  
            <!-- Whitelist Mode -->
            <div v-if="modelRestrictionMode === 'whitelist'">
              <ModelWhitelistSelector
                v-model="allowedModels"
                :platform="form.platform"
                :sync-credentials="syncPreviewCredentials"
                @upstream-models="importUpstreamModelsToMapping"
                @upstream-synced="upstreamModelsPreviewed = true"
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
              <ModelWhitelistSelector
                sync-only
                :model-value="modelMappingFromKeys"
                :platform="form.platform"
                :sync-credentials="syncPreviewCredentials"
                @upstream-models="importUpstreamModelsToMapping"
              />
  
              <div v-if="modelMappings.length > 0" class="mb-3 space-y-2">
                <div
                  v-for="(mapping, index) in modelMappings"
                  :key="'oauth-' + getModelMappingKey(mapping)"
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
  
              <UButton color="neutral" variant="soft" block icon="i-lucide-plus" class="mb-3" type="button" @click="addModelMapping">
                + {{ t('admin.accounts.addMapping') }}
              </UButton>
  
              <!-- Quick Add Buttons -->
              <div class="flex flex-wrap gap-2">
                <UButton
                  color="neutral"
                  variant="soft"
                  size="xs"
                  v-for="preset in presetMappings"
                  :key="'oauth-' + preset.label"
                  type="button"
                  @click="addPresetMapping(preset.from, preset.to)"
                >
                  + {{ preset.label }}
                </UButton>
              </div>
            </div>
          </template>
        </div>

        <div v-if="(form.type === 'apikey' || form.type === 'cloudflare') && form.platform !== 'antigravity'" class="space-y-4">
          <div class="pt-2">
            <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.modelRestriction') }}</label>
  
            <UAlert v-if="isOpenAIModelRestrictionDisabled" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.accounts.openai.modelRestrictionDisabledByPassthrough')" class="mb-3" />
  
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
                <ModelWhitelistSelector
                  v-model="allowedModels"
                  :platform="form.platform"
                  :sync-credentials="syncPreviewCredentials"
                  @upstream-models="importUpstreamModelsToMapping"
                  @upstream-synced="upstreamModelsPreviewed = true"
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
                <ModelWhitelistSelector
                  sync-only
                  :model-value="modelMappingFromKeys"
                  :platform="form.platform"
                  :sync-credentials="syncPreviewCredentials"
                  @upstream-models="importUpstreamModelsToMapping"
                />
  
              <!-- Model Mapping List -->
              <div v-if="modelMappings.length > 0" class="mb-3 space-y-2">
                <div
                  v-for="(mapping, index) in modelMappings"
                  :key="getModelMappingKey(mapping)"
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
                    v-for="preset in presetMappings"
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

        <div v-if="form.platform === 'anthropic' && accountCategory === 'bedrock'" class="space-y-4">
          <div class="pt-2">
            <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.modelRestriction') }}</label>
  
            <!-- Mode Toggle -->
            <div class="mb-4 flex gap-1 rounded-lg bg-elevated p-1">
              <UButton
                :color="modelRestrictionMode === 'whitelist' ? 'primary' : 'neutral'"
                :variant="modelRestrictionMode === 'whitelist' ? 'solid' : 'ghost'"
                size="sm"
                block
                class="flex-1"
                type="button"
                @click="modelRestrictionMode = 'whitelist'"
              >{{ t('admin.accounts.modelWhitelist') }}</UButton>
              <UButton
                :color="modelRestrictionMode === 'mapping' ? 'primary' : 'neutral'"
                :variant="modelRestrictionMode === 'mapping' ? 'solid' : 'ghost'"
                size="sm"
                block
                class="flex-1"
                type="button"
                @click="modelRestrictionMode = 'mapping'"
              >{{ t('admin.accounts.modelMapping') }}</UButton>
            </div>
  
            <!-- Whitelist Mode -->
            <div v-if="modelRestrictionMode === 'whitelist'">
              <ModelWhitelistSelector
                v-model="allowedModels"
                platform="anthropic"
                :sync-credentials="syncPreviewCredentials"
                @upstream-models="importUpstreamModelsToMapping"
                @upstream-synced="upstreamModelsPreviewed = true"
              />
              <p class="text-xs text-muted">
                {{ t('admin.accounts.selectedModels', { count: allowedModels.length }) }}
                <span v-if="allowedModels.length === 0">{{ t('admin.accounts.supportsAllModels') }}</span>
              </p>
            </div>
  
            <!-- Mapping Mode -->
            <div v-else class="space-y-3">
              <ModelWhitelistSelector
                sync-only
                :model-value="modelMappingFromKeys"
                platform="anthropic"
                :sync-credentials="syncPreviewCredentials"
                @upstream-models="importUpstreamModelsToMapping"
              />
              <div v-for="(mapping, index) in modelMappings" :key="index" class="flex items-center gap-2">
                <UInput v-model="mapping.from" type="text" :placeholder="t('admin.accounts.fromModel')" class="flex-1" />
                <span class="text-muted">→</span>
                <UInput v-model="mapping.to" type="text" :placeholder="t('admin.accounts.toModel')" class="flex-1" />
                <UButton
                  color="error"
                  variant="ghost"
                  square
                  size="sm"
                  icon="i-lucide-trash-2"
                  :aria-label="t('admin.accounts.formActions.remove')"
                  type="button"
                  @click="modelMappings.splice(index, 1)"
                />
              </div>
              <UButton color="neutral" variant="outline" type="button" @click="modelMappings.push({ from: '', to: '' })" class="text-sm">
                + {{ t('admin.accounts.addMapping') }}
              </UButton>
              <!-- Bedrock Preset Mappings -->
              <div class="flex flex-wrap gap-2">
                <UButton
                  color="neutral"
                  variant="soft"
                  size="xs"
                  v-for="preset in bedrockPresets"
                  :key="preset.from"
                  type="button"
                  @click="addPresetMapping(preset.from, preset.to)"
                >
                  + {{ preset.label }}
                </UButton>
              </div>
            </div>
          </div>
        </div>

        <h4 class="pt-2 text-sm font-semibold text-highlighted">{{ t('admin.accounts.formSections.scheduling') }}</h4>

        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <UFormField :label="t('admin.accounts.concurrency')">
            <UInput
              v-model.number="form.concurrency"
              type="number"
              min="1"
              @input="form.concurrency = Math.max(1, form.concurrency || 1)"
              class="w-full"
            />
          </UFormField>
          <UFormField :label="t('admin.accounts.loadFactor')" :help="t('admin.accounts.loadFactorHint')">
            <UInput
              v-model.number="form.load_factor"
              type="number"
              min="1"
              :placeholder="String(form.concurrency || 1)"
              @input="form.load_factor = (form.load_factor &amp;&amp; form.load_factor >= 1) ? form.load_factor : null"
              class="w-full"
            />
          </UFormField>
          <UFormField :label="t('admin.accounts.priority')" :help="t('admin.accounts.priorityHint')">
            <UInput
              v-model.number="form.priority"
              type="number"
              min="0"
              step="1"
              data-tour="account-form-priority"
              class="w-full"
            />
          </UFormField>
          <UFormField :label="t('admin.accounts.billingRateMultiplier')" :help="t('admin.accounts.billingRateMultiplierHint')">
            <UInput v-model.number="form.rate_multiplier" type="number" min="0" step="0.001" class="w-full" />
          </UFormField>
        </div>

        <div class="pt-2">
          <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.expiresAt') }}</label>
          <UInput v-model="expiresAtInput" type="datetime-local" class="w-full" />
          <div class="mt-2 flex gap-2">
            <UButton color="neutral" variant="outline" size="sm" type="button" @click="form.expires_at = getAccountExpiryTimestamp(1)">
              {{ t('payment.oneMonth') }}
            </UButton>
            <UButton color="neutral" variant="outline" size="sm" type="button" @click="form.expires_at = getAccountExpiryTimestamp(12)">
              {{ t('payment.oneYear') }}
            </UButton>
          </div>
          <p class="mt-1.5 text-xs text-muted">
            {{ t('admin.accounts.expiresAtHint') }}
            {{ t('admin.accounts.expiresAtTimezoneHint', { timezone: browserTimeZone }) }}
          </p>
        </div>

        <div class="pt-2">
          <!-- Mixed Scheduling (only for antigravity accounts) -->
          <div v-if="form.platform === 'antigravity'" class="flex items-center gap-2">
            <UCheckbox v-model="mixedScheduling" :label="t('admin.accounts.mixedScheduling')" />
            <div class="group relative">
              <span
                class="inline-flex h-4 w-4 cursor-help items-center justify-center rounded-full bg-accented text-xs text-muted hover:bg-elevated"
              >
                ?
              </span>
              <!-- Tooltip（向下显示避免被弹窗裁剪） -->
              <div
                class="pointer-events-none absolute left-0 top-full z-100 mt-1.5 w-72 rounded-sm bg-inverted px-3 py-2 text-xs text-inverted opacity-0 transition-opacity group-hover:opacity-100"
              >
                {{ t('admin.accounts.mixedSchedulingTooltip') }}
                <div
                  class="absolute bottom-full left-3 border-4 border-transparent border-b-gray-900"
                ></div>
              </div>
            </div>
          </div>
          <div v-if="form.platform === 'antigravity'" class="mt-3 flex items-center gap-2">
            <UCheckbox v-model="allowOverages" :label="t('admin.accounts.allowOverages')" />
            <div class="group relative">
              <span
                class="inline-flex h-4 w-4 cursor-help items-center justify-center rounded-full bg-accented text-xs text-muted hover:bg-elevated"
              >
                ?
              </span>
              <div
                class="pointer-events-none absolute left-0 top-full z-100 mt-1.5 w-72 rounded-sm bg-inverted px-3 py-2 text-xs text-inverted opacity-0 transition-opacity group-hover:opacity-100"
              >
                {{ t('admin.accounts.allowOveragesTooltip') }}
                <div
                  class="absolute bottom-full left-3 border-4 border-transparent border-b-gray-900"
                ></div>
              </div>
            </div>
          </div>
  
          <!-- Group Selection - 仅标准模式显示 -->
          <GroupSelector
            v-model="form.group_ids"
            :groups="groups"
            :platform="form.platform"
            :mixed-scheduling="mixedScheduling"
            data-tour="account-form-groups"
          />
        </div>
      </div>
      <div v-show="showWizardStep(3)" data-wizard-step="3" class="space-y-6">
        <h4 v-if="[(form.platform === 'anthropic' && (form.type === 'apikey' || form.type === 'bedrock')), (form.type === 'apikey' || form.type === 'cloudflare' || form.type === 'bedrock')].some(Boolean)" class="pt-2 text-sm font-semibold text-highlighted">{{ t('admin.accounts.formSections.limits') }}</h4>

        <!-- 配额控制 (Anthropic apikey/bedrock: 配额限制 + 亲和) -->
        <div
          v-if="form.platform === 'anthropic' && (form.type === 'apikey' || form.type === 'bedrock')"
          class="pt-2 space-y-4"
        >
          <div class="mb-3">
            <h3 class="block text-sm font-medium text-default text-base font-semibold">{{ t('admin.accounts.quotaControl.title') }}</h3>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.quotaControl.hint') }}
            </p>
          </div>
          <QuotaLimitCard
            :totalLimit="editQuotaLimit"
            :dailyLimit="editQuotaDailyLimit"
            :weeklyLimit="editQuotaWeeklyLimit"
            :quotaNotifyGlobalEnabled="quotaNotifyGlobalEnabled"
            :quotaNotifyDailyEnabled="quotaNotifyState.daily.enabled"
            :quotaNotifyDailyThreshold="quotaNotifyState.daily.threshold"
            :quotaNotifyDailyThresholdType="quotaNotifyState.daily.thresholdType"
            :quotaNotifyWeeklyEnabled="quotaNotifyState.weekly.enabled"
            :quotaNotifyWeeklyThreshold="quotaNotifyState.weekly.threshold"
            :quotaNotifyWeeklyThresholdType="quotaNotifyState.weekly.thresholdType"
            :quotaNotifyTotalEnabled="quotaNotifyState.total.enabled"
            :quotaNotifyTotalThreshold="quotaNotifyState.total.threshold"
            :quotaNotifyTotalThresholdType="quotaNotifyState.total.thresholdType"
            :dailyResetMode="editDailyResetMode"
            :dailyResetHour="editDailyResetHour"
            :weeklyResetMode="editWeeklyResetMode"
            :weeklyResetDay="editWeeklyResetDay"
            :weeklyResetHour="editWeeklyResetHour"
            :resetTimezone="editResetTimezone"
            @update:totalLimit="editQuotaLimit = $event"
            @update:dailyLimit="editQuotaDailyLimit = $event"
            @update:weeklyLimit="editQuotaWeeklyLimit = $event"
            @update:quotaNotifyDailyEnabled="quotaNotifyState.daily.enabled = $event"
            @update:quotaNotifyDailyThreshold="quotaNotifyState.daily.threshold = $event"
            @update:quotaNotifyDailyThresholdType="quotaNotifyState.daily.thresholdType = $event"
            @update:quotaNotifyWeeklyEnabled="quotaNotifyState.weekly.enabled = $event"
            @update:quotaNotifyWeeklyThreshold="quotaNotifyState.weekly.threshold = $event"
            @update:quotaNotifyWeeklyThresholdType="quotaNotifyState.weekly.thresholdType = $event"
            @update:quotaNotifyTotalEnabled="quotaNotifyState.total.enabled = $event"
            @update:quotaNotifyTotalThreshold="quotaNotifyState.total.threshold = $event"
            @update:quotaNotifyTotalThresholdType="quotaNotifyState.total.thresholdType = $event"
            @update:dailyResetMode="editDailyResetMode = $event"
            @update:dailyResetHour="editDailyResetHour = $event"
            @update:weeklyResetMode="editWeeklyResetMode = $event"
            @update:weeklyResetDay="editWeeklyResetDay = $event"
            @update:weeklyResetHour="editWeeklyResetHour = $event"
            @update:resetTimezone="editResetTimezone = $event"
          />
        </div>
  
        <!-- 配额控制 (非 Anthropic apikey/bedrock) -->
        <div
          v-else-if="form.type === 'apikey' || form.type === 'cloudflare' || form.type === 'bedrock'"
          class="pt-2 space-y-4"
        >
          <div class="mb-3">
            <h3 class="block text-sm font-medium text-default text-base font-semibold">{{ t('admin.accounts.quotaControl.title') }}</h3>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.quotaLimitHint') }}
            </p>
          </div>
          <QuotaLimitCard
            :totalLimit="editQuotaLimit"
            :dailyLimit="editQuotaDailyLimit"
            :weeklyLimit="editQuotaWeeklyLimit"
            :quotaNotifyGlobalEnabled="quotaNotifyGlobalEnabled"
            :quotaNotifyDailyEnabled="quotaNotifyState.daily.enabled"
            :quotaNotifyDailyThreshold="quotaNotifyState.daily.threshold"
            :quotaNotifyDailyThresholdType="quotaNotifyState.daily.thresholdType"
            :quotaNotifyWeeklyEnabled="quotaNotifyState.weekly.enabled"
            :quotaNotifyWeeklyThreshold="quotaNotifyState.weekly.threshold"
            :quotaNotifyWeeklyThresholdType="quotaNotifyState.weekly.thresholdType"
            :quotaNotifyTotalEnabled="quotaNotifyState.total.enabled"
            :quotaNotifyTotalThreshold="quotaNotifyState.total.threshold"
            :quotaNotifyTotalThresholdType="quotaNotifyState.total.thresholdType"
            :dailyResetMode="editDailyResetMode"
            :dailyResetHour="editDailyResetHour"
            :weeklyResetMode="editWeeklyResetMode"
            :weeklyResetDay="editWeeklyResetDay"
            :weeklyResetHour="editWeeklyResetHour"
            :resetTimezone="editResetTimezone"
            @update:totalLimit="editQuotaLimit = $event"
            @update:dailyLimit="editQuotaDailyLimit = $event"
            @update:weeklyLimit="editQuotaWeeklyLimit = $event"
            @update:quotaNotifyDailyEnabled="quotaNotifyState.daily.enabled = $event"
            @update:quotaNotifyDailyThreshold="quotaNotifyState.daily.threshold = $event"
            @update:quotaNotifyDailyThresholdType="quotaNotifyState.daily.thresholdType = $event"
            @update:quotaNotifyWeeklyEnabled="quotaNotifyState.weekly.enabled = $event"
            @update:quotaNotifyWeeklyThreshold="quotaNotifyState.weekly.threshold = $event"
            @update:quotaNotifyWeeklyThresholdType="quotaNotifyState.weekly.thresholdType = $event"
            @update:quotaNotifyTotalEnabled="quotaNotifyState.total.enabled = $event"
            @update:quotaNotifyTotalThreshold="quotaNotifyState.total.threshold = $event"
            @update:quotaNotifyTotalThresholdType="quotaNotifyState.total.thresholdType = $event"
            @update:dailyResetMode="editDailyResetMode = $event"
            @update:dailyResetHour="editDailyResetHour = $event"
            @update:weeklyResetMode="editWeeklyResetMode = $event"
            @update:weeklyResetDay="editWeeklyResetDay = $event"
            @update:weeklyResetHour="editWeeklyResetHour = $event"
            @update:resetTimezone="editResetTimezone = $event"
          />
        </div>

        <div v-if="(form.type === 'apikey' || form.type === 'cloudflare') && form.platform !== 'antigravity'" class="space-y-4">
          <!-- Pool Mode Section -->
          <h4 class="pt-2 text-sm font-semibold text-highlighted">{{ t('admin.accounts.formSections.errors') }}</h4>
  
          <div class="pt-2">
            <UFormField :label="t('admin.accounts.poolMode')" :description="t('admin.accounts.poolModeHint')" class="flex items-center justify-between gap-4 mb-3">
              <Toggle v-model="poolModeEnabled" />
            </UFormField>
            <UAlert v-if="poolModeEnabled" color="neutral" variant="subtle" icon="i-lucide-info" :description="t('admin.accounts.poolModeInfo')" />
            <div v-if="poolModeEnabled" class="mt-3">
              <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.poolModeRetryCount') }}</label>
              <UInput
                v-model.number="poolModeRetryCount"
                type="number"
                min="0"
                :max="MAX_POOL_MODE_RETRY_COUNT"
                step="1"
                class="w-full"
              />
              <p class="mt-1 text-xs text-muted">
                {{
                  t('admin.accounts.poolModeRetryCountHint', {
                    default: DEFAULT_POOL_MODE_RETRY_COUNT,
                    max: MAX_POOL_MODE_RETRY_COUNT
                  })
                }}
              </p>
            </div>
            <div v-if="poolModeEnabled" class="mt-3">
              <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.poolModeRetryStatusCodes') }}</label>
              <UInput
                v-model="poolModeRetryStatusCodesInput"
                type="text"
                :placeholder="DEFAULT_POOL_MODE_RETRY_STATUS_CODES.join(', ')"
                class="w-full"
              />
              <p class="mt-1 text-xs text-muted">
                {{ t('admin.accounts.poolModeRetryStatusCodesHint', { default: DEFAULT_POOL_MODE_RETRY_STATUS_CODES.join(', ') }) }}
              </p>
            </div>
          </div>
  
          <!-- Custom Error Codes Section -->
          <div class="pt-2">
            <UFormField :label="t('admin.accounts.customErrorCodes')" :description="t('admin.accounts.customErrorCodesHint')" class="flex items-center justify-between gap-4 mb-3">
              <Toggle v-model="customErrorCodesEnabled" />
            </UFormField>
  
            <div v-if="customErrorCodesEnabled" class="space-y-3">
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
                  v-model.number="customErrorCodeInput"
                  type="number"
                  min="100"
                  max="599"
                  :placeholder="t('admin.accounts.enterErrorCode')"
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
  
          <!-- Header Override Section (eligible API-key platforms) -->
          <h4 v-if="(isHeaderOverrideCapable(form.platform, 'apikey'))" class="pt-2 text-sm font-semibold text-highlighted">{{ t('admin.accounts.formSections.forwarding') }}</h4>
  
          <div
            v-if="isHeaderOverrideCapable(form.platform, 'apikey')"
            class="pt-2"
          >
            <UFormField :label="t('admin.accounts.headerOverride.title')" :description="t('admin.accounts.headerOverride.hint')" class="flex items-center justify-between gap-4 mb-3">
              <Toggle v-model="headerOverrideEnabled" />
            </UFormField>
  
            <div v-if="headerOverrideEnabled" class="space-y-3">
              <UAlert color="neutral" variant="subtle" icon="i-lucide-info" :description="t('admin.accounts.headerOverride.info')" />
  
              <HeaderOverrideEditor
                :rows="headerOverrideRows"
                @update:rows="headerOverrideRows = $event"
              />
            </div>
          </div>
        </div>

        <div v-if="form.platform === 'anthropic' && accountCategory === 'bedrock'" class="space-y-4">
          <!-- Pool Mode Section for Bedrock -->
          <h4 class="pt-2 text-sm font-semibold text-highlighted">{{ t('admin.accounts.formSections.errors') }}</h4>
  
          <div class="pt-2">
            <UFormField :label="t('admin.accounts.poolMode')" :description="t('admin.accounts.poolModeHint')" class="flex items-center justify-between gap-4 mb-3">
              <Toggle v-model="poolModeEnabled" />
            </UFormField>
            <UAlert v-if="poolModeEnabled" color="neutral" variant="subtle" icon="i-lucide-info" :description="t('admin.accounts.poolModeInfo')" />
            <div v-if="poolModeEnabled" class="mt-3">
              <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.poolModeRetryCount') }}</label>
              <UInput
                v-model.number="poolModeRetryCount"
                type="number"
                min="0"
                :max="MAX_POOL_MODE_RETRY_COUNT"
                step="1"
                class="w-full"
              />
              <p class="mt-1 text-xs text-muted">
                {{
                  t('admin.accounts.poolModeRetryCountHint', {
                    default: DEFAULT_POOL_MODE_RETRY_COUNT,
                    max: MAX_POOL_MODE_RETRY_COUNT
                  })
                }}
              </p>
            </div>
            <div v-if="poolModeEnabled" class="mt-3">
              <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.poolModeRetryStatusCodes') }}</label>
              <UInput
                v-model="poolModeRetryStatusCodesInput"
                type="text"
                :placeholder="DEFAULT_POOL_MODE_RETRY_STATUS_CODES.join(', ')"
                class="w-full"
              />
              <p class="mt-1 text-xs text-muted">
                {{ t('admin.accounts.poolModeRetryStatusCodesHint', { default: DEFAULT_POOL_MODE_RETRY_STATUS_CODES.join(', ') }) }}
              </p>
            </div>
          </div>
        </div>

        <h4 v-if="(form.platform === 'grok' && isOAuthFlow)" class="pt-2 text-sm font-semibold text-highlighted">{{ t('admin.accounts.formSections.forwarding') }}</h4>

        <!-- Grok OAuth Header Override (OAuth 类型没有 apikey 容器，需要独立区域) -->
        <div
          v-if="form.platform === 'grok' && isOAuthFlow"
          class="pt-2"
        >
          <UFormField :label="t('admin.accounts.headerOverride.title')" :description="t('admin.accounts.headerOverride.hint')" class="flex items-center justify-between gap-4 mb-3">
            <Toggle v-model="headerOverrideEnabled" />
          </UFormField>
  
          <div v-if="headerOverrideEnabled" class="space-y-3">
            <UAlert color="neutral" variant="subtle" icon="i-lucide-info" :description="t('admin.accounts.headerOverride.info')" />
  
            <HeaderOverrideEditor
              :rows="headerOverrideRows"
              @update:rows="headerOverrideRows = $event"
            />
          </div>
        </div>

        <h4 class="pt-2 text-sm font-semibold text-highlighted">{{ t('admin.accounts.formSections.network') }}</h4>

        <div>
          <div class="mb-1 flex items-center gap-2">
            <label class="block text-sm font-medium text-default">{{ t('admin.accounts.proxy') }}</label>
            <ProxyAdBanner />
          </div>
          <ProxySelector v-model="form.proxy_id" :proxies="proxies" />
        </div>

        <UpstreamRequestIdHeaderField
          v-model="upstreamRequestIdHeader"
          :platform="form.platform"
          :type="form.type"
        />

        <UCollapsible :unmount-on-hide="false">
          <UButton type="button" color="neutral" variant="ghost" :label="t('admin.accounts.advancedOptions')" trailing-icon="i-lucide-chevron-down" class="group -ms-2.5 font-semibold text-highlighted" :ui="{ trailingIcon: 'transition-transform duration-200 group-data-[state=open]:rotate-180' }" />
          <template #content>
            <div class="space-y-6 pt-4">
              <h4 class="pt-2 text-sm font-semibold text-highlighted">{{ t('admin.accounts.formSections.scheduling') }}</h4>
  
              <div>
          <UFormField :label="t('admin.accounts.autoPauseOnExpired')" :description="t('admin.accounts.autoPauseOnExpiredDesc')" class="flex items-center justify-between gap-4">
            <Toggle v-model="autoPauseOnExpired" />
          </UFormField>
        </div>
  
              <div class="pt-2 space-y-4">
          <UFormField :label="t('admin.accounts.tempUnschedulable.title')" :description="t('admin.accounts.tempUnschedulable.hint')" class="flex items-center justify-between gap-4 mb-3">
            <Toggle v-model="tempUnschedEnabled" />
          </UFormField>
  
          <div v-if="tempUnschedEnabled" class="space-y-3">
            <UAlert color="neutral" variant="subtle" icon="i-lucide-info" :description="t('admin.accounts.tempUnschedulable.notice')" />
  
            <div class="flex flex-wrap gap-2">
              <UButton
                color="neutral"
                variant="soft"
                size="xs"
                v-for="preset in tempUnschedPresets"
                :key="preset.label"
                type="button"
                @click="addTempUnschedRule(preset.rule)"
              >
                + {{ preset.label }}
              </UButton>
            </div>
  
            <div v-if="tempUnschedRules.length > 0" class="space-y-3">
              <div
                v-for="(rule, index) in tempUnschedRules"
                :key="getTempUnschedRuleKey(rule)"
               
              >
                <div class="mb-2 flex items-center justify-between">
                  <span class="text-xs font-medium text-muted">
                    {{ t('admin.accounts.tempUnschedulable.ruleIndex', { index: index + 1 }) }}
                  </span>
                  <div class="flex items-center gap-2">
                    <UButton
                      color="neutral"
                      variant="ghost"
                      square
                      size="xs"
                      icon="i-lucide-chevron-up"
                      type="button"
                      :disabled="index === 0"
                      @click="moveTempUnschedRule(index, -1)"
                    />
                    <UButton
                      color="neutral"
                      variant="ghost"
                      square
                      size="xs"
                      icon="i-lucide-chevron-down"
                      type="button"
                      :disabled="index === tempUnschedRules.length - 1"
                      @click="moveTempUnschedRule(index, 1)"
                    />
                    <UButton
                      color="error"
                      variant="ghost"
                      square
                      size="xs"
                      icon="i-lucide-x"
                      :aria-label="t('admin.accounts.formActions.remove')"
                      type="button"
                      @click="removeTempUnschedRule(index)"
                    />
                  </div>
                </div>
  
                <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <UFormField :label="t('admin.accounts.tempUnschedulable.errorCode')">
                    <UInput
                      v-model.number="rule.error_code"
                      type="number"
                      min="100"
                      max="599"
                      :placeholder="t('admin.accounts.tempUnschedulable.errorCodePlaceholder')"
                      class="w-full"
                    />
                  </UFormField>
                  <UFormField :label="t('admin.accounts.tempUnschedulable.durationMinutes')">
                    <UInput
                      v-model.number="rule.duration_minutes"
                      type="number"
                      min="1"
                      :placeholder="t('admin.accounts.tempUnschedulable.durationPlaceholder')"
                      class="w-full"
                    />
                  </UFormField>
                  <UFormField class="sm:col-span-2" :label="t('admin.accounts.tempUnschedulable.keywords')" :help="t('admin.accounts.tempUnschedulable.keywordsHint')">
                    <UInput
                      v-model="rule.keywords"
                      type="text"
                      :placeholder="t('admin.accounts.tempUnschedulable.keywordsPlaceholder')"
                      class="w-full"
                    />
                  </UFormField>
                  <UFormField class="sm:col-span-2" :label="t('admin.accounts.tempUnschedulable.description')">
                    <UInput
                      v-model="rule.description"
                      type="text"
                      :placeholder="t('admin.accounts.tempUnschedulable.descriptionPlaceholder')"
                      class="w-full"
                    />
                  </UFormField>
                </div>
              </div>
            </div>
  
            <UButton color="neutral" variant="soft" block icon="i-lucide-plus" type="button" @click="addTempUnschedRule()">{{ t('admin.accounts.tempUnschedulable.addRule') }}</UButton>
          </div>
        </div>
  
              <h4 v-if="[(form.platform === 'anthropic' || form.platform === 'antigravity'), (form.platform === 'openai'), (form.platform === 'openai' && form.type === 'oauth'), (form.platform === 'openai' && (accountCategory === 'oauth-based' || accountCategory === 'apikey')), (form.platform === 'anthropic' && accountCategory === 'apikey'), (form.platform === 'anthropic' && accountCategory === 'apikey'), (form.platform === 'anthropic' && accountCategory === 'apikey' && webSearchGlobalEnabled), (form.platform === 'openai' && accountCategory === 'oauth-based'), (form.platform === 'openai' && accountCategory === 'oauth-based'), (form.platform === 'openai' && (accountCategory === 'oauth-based' || accountCategory === 'apikey')), (form.platform === 'openai' && accountCategory === 'apikey'), (form.platform === 'openai' && accountCategory === 'apikey')].some(Boolean)" class="pt-2 text-sm font-semibold text-highlighted">{{ t('admin.accounts.formSections.forwarding') }}</h4>
  
              <!-- Intercept Warmup Requests (Anthropic/Antigravity) -->
        <div
          v-if="form.platform === 'anthropic' || form.platform === 'antigravity'"
          class="pt-2"
        >
          <UFormField :label="t('admin.accounts.interceptWarmupRequests')" :description="t('admin.accounts.interceptWarmupRequestsDesc')" class="flex items-center justify-between gap-4">
            <Toggle v-model="interceptWarmupRequests" />
          </UFormField>
        </div>
  
              <!-- OpenAI 自动透传开关（OAuth/API Key） -->
        <div
          v-if="form.platform === 'openai'"
          class="pt-2"
        >
          <UFormField :label="t('admin.accounts.openai.oauthPassthrough')" :description="t('admin.accounts.openai.oauthPassthroughDesc')" class="flex items-center justify-between gap-4">
            <Toggle v-model="openaiPassthroughEnabled" />
          </UFormField>
        </div>
  
              <!-- OpenAI Codex namespace 工具摊平（兼容开关，仅 OAuth） -->
        <div
          v-if="form.platform === 'openai' && form.type === 'oauth'"
          class="pt-2"
        >
          <UFormField :label="t('admin.accounts.openai.flattenNamespaces')" :description="t('admin.accounts.openai.flattenNamespacesDesc')" class="flex items-center justify-between gap-4">
            <Toggle v-model="openaiFlattenNamespacesEnabled" data-testid="create-openai-flatten-namespaces-toggle" />
          </UFormField>
        </div>
  
              <!-- OpenAI WS Mode 三态（off/ctx_pool/passthrough） -->
        <div
          v-if="form.platform === 'openai' && (accountCategory === 'oauth-based' || accountCategory === 'apikey')"
          data-testid="create-openai-ws-mode"
          class="pt-2"
        >
          <div class="flex items-center justify-between">
            <div>
              <label class="block text-sm font-medium text-default">{{ t('admin.accounts.openai.wsMode') }}</label>
              <p class="mt-1 text-xs text-muted">
                {{ t('admin.accounts.openai.wsModeDesc') }}
              </p>
              <p class="mt-1 text-xs text-muted">
                {{ t(openAIWSModeConcurrencyHintKey) }}
              </p>
            </div>
            <div class="w-52">
              <Select v-model="openaiResponsesWebSocketV2Mode" :options="openAIWSModeOptions" />
            </div>
          </div>
        </div>
  
              <!-- Anthropic API Key 自动透传开关 -->
        <div
          v-if="form.platform === 'anthropic' && accountCategory === 'apikey'"
          class="pt-2"
        >
          <UFormField :label="t('admin.accounts.anthropic.apiKeyPassthrough')" :description="t('admin.accounts.anthropic.apiKeyPassthroughDesc')" class="flex items-center justify-between gap-4">
            <Toggle v-model="anthropicPassthroughEnabled" />
          </UFormField>
        </div>
  
              <div
          v-if="form.platform === 'anthropic' && accountCategory === 'apikey'"
          class="pt-2"
        >
          <div class="flex items-center justify-between gap-4">
            <div>
              <label class="block text-sm font-medium text-default">{{ t('admin.accounts.anthropic.apiKeyAuthScheme') }}</label>
              <p class="mt-1 text-xs text-muted">
                {{ t('admin.accounts.anthropic.apiKeyAuthSchemeDesc') }}
              </p>
            </div>
            <Select
              v-model="anthropicAPIKeyAuthScheme"
              :options="[
                { value: 'x_api_key', label: t('admin.accounts.anthropic.apiKeyAuthSchemeXApiKey') },
                { value: 'authorization_bearer', label: t('admin.accounts.anthropic.apiKeyAuthSchemeBearer') }
              ]"
              class="w-52"
            />
          </div>
        </div>
  
              <!-- Anthropic API Key: Web Search Emulation (hidden when global disabled) -->
        <div
          v-if="form.platform === 'anthropic' && accountCategory === 'apikey' && webSearchGlobalEnabled"
          class="pt-2"
        >
          <div class="flex items-center justify-between">
            <div>
              <label class="block text-sm font-medium text-default">{{ t('admin.accounts.anthropic.webSearchEmulation') }}</label>
              <p class="mt-1 text-xs text-muted">
                {{ t('admin.accounts.anthropic.webSearchEmulationDesc') }}
              </p>
            </div>
            <Select
              v-model="webSearchEmulationMode"
              :options="[
                { value: 'default', label: t('admin.accounts.anthropic.webSearchDefault') },
                { value: 'enabled', label: t('admin.accounts.anthropic.webSearchEnabled') },
                { value: 'disabled', label: t('admin.accounts.anthropic.webSearchDisabled') }
              ]"
              class="w-24"
            />
          </div>
        </div>
  
              <div
          v-if="form.platform === 'openai' && accountCategory === 'oauth-based'"
          class="pt-2"
        >
          <UFormField :label="t('admin.accounts.openai.codexCLIOnly')" :description="t('admin.accounts.openai.codexCLIOnlyDesc')" class="flex items-center justify-between gap-4">
            <Toggle v-model="codexCLIOnlyEnabled" />
          </UFormField>
          <UFormField v-if="codexCLIOnlyEnabled" :label="t('admin.accounts.openai.codexCLIOnlyAppServer')" :description="t('admin.accounts.openai.codexCLIOnlyAppServerDesc')" class="flex items-center justify-between gap-4 mt-4">
            <Toggle v-model="codexCLIOnlyAppServerEnabled" />
          </UFormField>
        </div>
  
              <!-- Codex 指纹收敛模式（仅 OpenAI OAuth） -->
        <div
          v-if="form.platform === 'openai' && accountCategory === 'oauth-based'"
          class="pt-2"
        >
          <div class="flex items-center justify-between gap-4">
            <div class="min-w-0">
              <label class="block text-sm font-medium text-default">{{ t('admin.accounts.openai.codexFingerprintMode') }}</label>
              <p class="mt-1 text-xs text-muted">
                {{ t('admin.accounts.openai.codexFingerprintModeDesc') }}
              </p>
            </div>
            <div class="w-52 flex-shrink-0">
              <Select v-model="codexFingerprintMode" data-testid="create-codex-fingerprint-mode-select" :options="codexFingerprintModeOptions" />
            </div>
          </div>
        </div>
  
              <!-- OpenAI Compact 能力配置 -->
        <div
          v-if="form.platform === 'openai' && (accountCategory === 'oauth-based' || accountCategory === 'apikey')"
          class="pt-2 space-y-4"
        >
          <div class="flex items-center justify-between">
            <div>
              <label class="block text-sm font-medium text-default">{{ t('admin.accounts.openai.compactMode') }}</label>
              <p class="mt-1 text-xs text-muted">
                {{ t('admin.accounts.openai.compactModeDesc') }}
              </p>
            </div>
            <div class="w-44">
              <Select v-model="openAICompactMode" :options="openAICompactModeOptions" />
            </div>
          </div>
          <div>
            <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.openai.compactModelMapping') }}</label>
            <p class="mt-1.5 text-xs text-muted">{{ t('admin.accounts.openai.compactModelMappingDesc') }}</p>
            <div v-if="openAICompactModelMappings.length > 0" class="mb-3 space-y-2">
              <div
                v-for="(mapping, index) in openAICompactModelMappings"
                :key="getOpenAICompactModelMappingKey(mapping)"
                class="flex items-center gap-2"
              >
                <UInput v-model="mapping.from" type="text" :placeholder="t('admin.accounts.fromModel')" class="flex-1" />
                <span class="text-muted">→</span>
                <UInput v-model="mapping.to" type="text" :placeholder="t('admin.accounts.toModel')" class="flex-1" />
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
            <UButton color="neutral" variant="outline" type="button" @click="addOpenAICompactModelMapping" class="text-sm">
              + {{ t('admin.accounts.addMapping') }}
            </UButton>
          </div>
        </div>
  
              <!-- OpenAI APIKey Responses API support mode -->
        <div
          v-if="form.platform === 'openai' && accountCategory === 'apikey'"
          class="space-y-4 pt-2"
        >
          <div class="flex items-center justify-between gap-4">
            <div>
              <label class="block text-sm font-medium text-default">{{ t('admin.accounts.openai.responsesMode') }}</label>
              <p class="mt-1 text-xs text-muted">
                {{ t('admin.accounts.openai.responsesModeDesc') }}
              </p>
            </div>
            <div class="w-56">
              <Select
                v-model="openAIResponsesMode"
                :options="openAIResponsesModeOptions"
                :disabled="!openAITextGenerationCapabilityEnabled"
                data-testid="openai-responses-mode-select"
              />
            </div>
          </div>
          <p
            v-if="!openAITextGenerationCapabilityEnabled"
            class="rounded-lg bg-warning/10 px-3 py-2 text-xs text-warning"
            data-testid="openai-responses-mode-not-applicable"
          >
            {{ t('admin.accounts.openai.responsesModeTextDisabledHint') }}
          </p>
          <div>
            <label class="mb-1.5 block text-sm font-medium text-default mb-2">{{ t('admin.accounts.openai.endpointCapabilities') }}</label>
            <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
              <UCheckbox
                v-for="option in openAIEndpointCapabilityOptions"
                :key="option.value"
                :data-testid="`openai-endpoint-capability-${option.value}`"
                :model-value="openAIEndpointCapabilities.includes(option.value)"
                @update:model-value="toggleOpenAIEndpointCapability(option.value)"
                :label="option.label"
              />
            </div>
            <p class="mt-1.5 text-xs text-muted">{{ t('admin.accounts.openai.endpointCapabilitiesDesc') }}</p>
          </div>
        </div>
  
              <!-- OpenAI APIKey images: backfill b64_json from url -->
        <UFormField v-if="form.platform === 'openai' && accountCategory === 'apikey'" :label="t('admin.accounts.openai.imagesUrlToB64Json')" :description="t('admin.accounts.openai.imagesUrlToB64JsonDesc')" class="flex items-center justify-between gap-4">
          <Toggle v-model="openAIImagesUrlToB64JsonEnabled" data-testid="openai-images-url-to-b64-json-toggle" />
        </UFormField>
  
              <h4 v-if="(form.platform === 'openai' && !hideAccountLongContextBilling && (accountCategory === 'oauth-based' || accountCategory === 'apikey'))" class="pt-2 text-sm font-semibold text-highlighted">{{ t('admin.accounts.formSections.billing') }}</h4>
  
              <!-- OpenAI API 长上下文计费开关 -->
        <div
          v-if="form.platform === 'openai' && !hideAccountLongContextBilling && (accountCategory === 'oauth-based' || accountCategory === 'apikey')"
          class="pt-2"
        >
          <UFormField :label="t('admin.accounts.openai.longContextBilling')" :description="t('admin.accounts.openai.longContextBillingDesc')" class="flex items-center justify-between gap-4">
            <Toggle :model-value="openAILongContextBillingEnabled" @update:model-value="toggleOpenAILongContextBilling()" data-testid="openai-long-context-billing-toggle" />
          </UFormField>
        </div>
  
              <h4 v-if="(form.platform === 'anthropic' && accountCategory === 'oauth-based')" class="pt-2 text-sm font-semibold text-highlighted">{{ t('admin.accounts.formSections.limits') }}</h4>
  
              <!-- 配额控制 (Anthropic OAuth/SetupToken: 亲和 + 窗口费用 + 会话 + RPM 等) -->
        <div
          v-if="form.platform === 'anthropic' && accountCategory === 'oauth-based'"
          class="pt-2 space-y-4"
        >
          <div class="mb-3">
            <h3 class="block text-sm font-medium text-default text-base font-semibold">{{ t('admin.accounts.quotaControl.title') }}</h3>
            <p class="mt-1 text-xs text-muted">
              {{ t('admin.accounts.quotaControl.hint') }}
            </p>
          </div>
  
          <!-- Window Cost Limit -->
          <div class="space-y-4">
            <UFormField :label="t('admin.accounts.quotaControl.windowCost.label')" :description="t('admin.accounts.quotaControl.windowCost.hint')" class="flex items-center justify-between gap-4 mb-3">
              <Toggle v-model="windowCostEnabled" />
            </UFormField>
  
            <div v-if="windowCostEnabled" class="grid grid-cols-2 gap-4">
              <div>
                <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.quotaControl.windowCost.limit') }}</label>
                <div class="relative">
                  <span class="absolute left-3 top-1/2 -translate-y-1/2 text-muted">$</span>
                  <UInput
                    v-model.number="windowCostLimit"
                    type="number"
                    min="0"
                    step="1"
                    :placeholder="t('admin.accounts.quotaControl.windowCost.limitPlaceholder')"
                    class="w-full"
                  />
                </div>
                <p class="mt-1.5 text-xs text-muted">{{ t('admin.accounts.quotaControl.windowCost.limitHint') }}</p>
              </div>
              <div>
                <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.quotaControl.windowCost.stickyReserve') }}</label>
                <div class="relative">
                  <span class="absolute left-3 top-1/2 -translate-y-1/2 text-muted">$</span>
                  <UInput
                    v-model.number="windowCostStickyReserve"
                    type="number"
                    min="0"
                    step="1"
                    :placeholder="t('admin.accounts.quotaControl.windowCost.stickyReservePlaceholder')"
                    class="w-full"
                  />
                </div>
                <p class="mt-1.5 text-xs text-muted">{{ t('admin.accounts.quotaControl.windowCost.stickyReserveHint') }}</p>
              </div>
            </div>
          </div>
  
          <!-- Session Limit -->
          <div class="space-y-4">
            <UFormField :label="t('admin.accounts.quotaControl.sessionLimit.label')" :description="t('admin.accounts.quotaControl.sessionLimit.hint')" class="flex items-center justify-between gap-4 mb-3">
              <Toggle v-model="sessionLimitEnabled" />
            </UFormField>
  
            <div v-if="sessionLimitEnabled" class="grid grid-cols-2 gap-4">
              <UFormField :label="t('admin.accounts.quotaControl.sessionLimit.maxSessions')" :help="t('admin.accounts.quotaControl.sessionLimit.maxSessionsHint')">
                <UInput
                  v-model.number="maxSessions"
                  type="number"
                  min="1"
                  step="1"
                  :placeholder="t('admin.accounts.quotaControl.sessionLimit.maxSessionsPlaceholder')"
                  class="w-full"
                />
              </UFormField>
              <div>
                <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.quotaControl.sessionLimit.idleTimeout') }}</label>
                <div class="relative">
                  <UInput
                    v-model.number="sessionIdleTimeout"
                    type="number"
                    min="1"
                    step="1"
                    :placeholder="t('admin.accounts.quotaControl.sessionLimit.idleTimeoutPlaceholder')"
                    class="w-full"
                  />
                  <span class="absolute right-3 top-1/2 -translate-y-1/2 text-muted">{{ t('common.minutes') }}</span>
                </div>
                <p class="mt-1.5 text-xs text-muted">{{ t('admin.accounts.quotaControl.sessionLimit.idleTimeoutHint') }}</p>
              </div>
            </div>
          </div>
  
          <!-- RPM Limit -->
          <div class="space-y-4">
            <UFormField :label="t('admin.accounts.quotaControl.rpmLimit.label')" :description="t('admin.accounts.quotaControl.rpmLimit.hint')" class="flex items-center justify-between gap-4 mb-3">
              <Toggle v-model="rpmLimitEnabled" />
            </UFormField>
  
            <div v-if="rpmLimitEnabled" class="space-y-4">
              <UFormField :label="t('admin.accounts.quotaControl.rpmLimit.baseRpm')" :help="t('admin.accounts.quotaControl.rpmLimit.baseRpmHint')">
                <UInput
                  v-model.number="baseRpm"
                  type="number"
                  min="1"
                  max="1000"
                  step="1"
                  :placeholder="t('admin.accounts.quotaControl.rpmLimit.baseRpmPlaceholder')"
                  class="w-full"
                />
              </UFormField>
  
              <div>
                <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.quotaControl.rpmLimit.strategy') }}</label>
                <div class="flex gap-1 rounded-lg bg-elevated p-1">
                  <UButton
                    :color="rpmStrategy === 'tiered' ? 'primary' : 'neutral'"
                    :variant="rpmStrategy === 'tiered' ? 'solid' : 'ghost'"
                    size="sm"
                    block
                    class="flex-1"
                    type="button"
                    @click="rpmStrategy = 'tiered'"
                  >
                    <div class="text-center">
                      <div>{{ t('admin.accounts.quotaControl.rpmLimit.strategyTiered') }}</div>
                      <div class="mt-0.5 text-[10px] opacity-70">{{ t('admin.accounts.quotaControl.rpmLimit.strategyTieredHint') }}</div>
                    </div>
                  </UButton>
                  <UButton
                    :color="rpmStrategy === 'sticky_exempt' ? 'primary' : 'neutral'"
                    :variant="rpmStrategy === 'sticky_exempt' ? 'solid' : 'ghost'"
                    size="sm"
                    block
                    class="flex-1"
                    type="button"
                    @click="rpmStrategy = 'sticky_exempt'"
                  >
                    <div class="text-center">
                      <div>{{ t('admin.accounts.quotaControl.rpmLimit.strategyStickyExempt') }}</div>
                      <div class="mt-0.5 text-[10px] opacity-70">{{ t('admin.accounts.quotaControl.rpmLimit.strategyStickyExemptHint') }}</div>
                    </div>
                  </UButton>
                </div>
              </div>
  
              <UFormField v-if="rpmStrategy === 'tiered'" :label="t('admin.accounts.quotaControl.rpmLimit.stickyBuffer')" :help="t('admin.accounts.quotaControl.rpmLimit.stickyBufferHint')">
                <UInput
                  v-model.number="rpmStickyBuffer"
                  type="number"
                  min="1"
                  step="1"
                  :placeholder="t('admin.accounts.quotaControl.rpmLimit.stickyBufferPlaceholder')"
                  class="w-full"
                />
              </UFormField>
  
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
                  @click="userMsgQueueMode = opt.value"
                >{{ opt.label }}</UButton>
              </div>
            </div>
          </div>
  
          <!-- TLS Fingerprint -->
          <div class="space-y-4">
            <UFormField :label="t('admin.accounts.quotaControl.tlsFingerprint.label')" :description="t('admin.accounts.quotaControl.tlsFingerprint.hint')" class="flex items-center justify-between gap-4">
              <Toggle v-model="tlsFingerprintEnabled" />
            </UFormField>
            <!-- Profile selector -->
            <div v-if="tlsFingerprintEnabled" class="mt-3">
              <Select
                v-model="tlsFingerprintProfileId"
                :options="[
                  { value: null, label: t('admin.accounts.quotaControl.tlsFingerprint.defaultProfile') },
                  ...(tlsFingerprintProfiles.length > 0 ? [{ value: -1, label: t('admin.accounts.quotaControl.tlsFingerprint.randomProfile') }] : []),
                  ...tlsFingerprintProfiles.map((p) => ({ value: p.id, label: p.name }))
                ]"
              />
            </div>
          </div>
  
          <!-- Session ID Masking -->
          <div class="space-y-4">
            <UFormField :label="t('admin.accounts.quotaControl.sessionIdMasking.label')" :description="t('admin.accounts.quotaControl.sessionIdMasking.hint')" class="flex items-center justify-between gap-4">
              <Toggle v-model="sessionIdMaskingEnabled" />
            </UFormField>
          </div>
  
          <!-- Cache TTL Override -->
          <div class="space-y-4">
            <UFormField :label="t('admin.accounts.quotaControl.cacheTTLOverride.label')" :description="t('admin.accounts.quotaControl.cacheTTLOverride.hint')" class="flex items-center justify-between gap-4">
              <Toggle v-model="cacheTTLOverrideEnabled" />
            </UFormField>
            <div v-if="cacheTTLOverrideEnabled" class="mt-3">
              <label class="mb-1.5 block text-sm font-medium text-default text-xs">{{ t('admin.accounts.quotaControl.cacheTTLOverride.target') }}</label>
              <Select
                v-model="cacheTTLOverrideTarget"
                :options="[{ value: '5m', label: '5m' }, { value: '1h', label: '1h' }]"
                class="mt-1"
              />
              <p class="mt-1 text-xs text-muted">
                {{ t('admin.accounts.quotaControl.cacheTTLOverride.targetHint') }}
              </p>
            </div>
          </div>
  
          <!-- Custom Base URL Relay -->
          <div class="space-y-4">
            <UFormField :label="t('admin.accounts.quotaControl.customBaseUrl.label')" :description="t('admin.accounts.quotaControl.customBaseUrl.hint')" class="flex items-center justify-between gap-4">
              <Toggle v-model="customBaseUrlEnabled" />
            </UFormField>
            <div v-if="customBaseUrlEnabled" class="mt-3">
              <UInput
                v-model="customBaseUrl"
                type="text"
                :placeholder="t('admin.accounts.quotaControl.customBaseUrl.urlHint')"
                class="w-full"
              />
            </div>
          </div>
        </div>
            </div>
          </template>
        </UCollapsible>
      </div>
      <div v-show="showWizardStep(4)" data-wizard-step="4" class="space-y-4">
        <p class="text-sm text-muted">{{ t('admin.accounts.wizard.confirmHint') }}</p>
        <dl class="divide-y divide-default rounded-md border border-default">
          <div v-for="row in wizardSummary" :key="row.label" class="flex items-start justify-between gap-4 px-3 py-2 text-sm">
            <dt class="text-muted">{{ row.label }}</dt>
            <dd class="text-end text-highlighted">{{ row.value }}</dd>
          </div>
        </dl>
      </div>

    </form>

    <!-- Step 2: OAuth Authorization -->
    <div v-else class="space-y-5">
      <OAuthAuthorizationFlow
        ref="oauthFlowRef"
        :add-method="form.platform === 'anthropic' ? addMethod : 'oauth'"
        :auth-url="currentAuthUrl"
        :session-id="currentSessionId"
        :loading="currentOAuthLoading"
        :error="currentOAuthError"
        :show-help="form.platform === 'anthropic'"
        :show-proxy-warning="form.platform !== 'openai' && form.platform !== 'grok' && !!form.proxy_id"
        :allow-multiple="form.platform === 'anthropic'"
        :show-cookie-option="form.platform === 'anthropic'"
        :show-refresh-token-option="form.platform === 'openai' || form.platform === 'antigravity' || form.platform === 'grok'"
        :show-mobile-refresh-token-option="form.platform === 'openai'"
        :show-session-token-option="false"
        :show-access-token-option="false"
        :show-codex-session-import-option="form.platform === 'openai'"
        :show-agent-identity-option="form.platform === 'openai'"
        :show-codex-pat-option="form.platform === 'openai'"
        :show-sso-option="form.platform === 'grok'"
        :show-email-password-option="false"
        :show-manual-option="true"
        :initial-input-method="'manual'"
        :platform="form.platform"
        :show-project-id="geminiOAuthType === 'code_assist'"
        @generate-url="handleGenerateUrl"
        @cookie-auth="handleCookieAuth"
        @validate-refresh-token="handleValidateRefreshToken"
        @validate-mobile-refresh-token="handleOpenAIValidateMobileRT"
        @validate-session-token="handleValidateSessionToken"
        @import-codex-session="handleOpenAIImportCodexSession"
        @import-codex-pat="handleOpenAIImportCodexPAT"
        @import-sso="handleGrokImportSSO"
        @authorize-password="handleGrokAuthorizePassword"
      />

    </div>

    <template #footer>
      <div v-if="step === 1" class="flex justify-end gap-2">
        <UButton
          v-if="wizardStep === wizardSteps[0]?.index || wizardShowsAll"
          color="neutral"
          variant="subtle"
          type="button"
          data-testid="wizard-cancel"
          @click="handleClose"
        >
          {{ t('common.cancel') }}
        </UButton>
        <UButton v-else color="neutral" variant="subtle" type="button" data-testid="wizard-back" @click="wizardBack">
          {{ t('common.back') }}
        </UButton>
        <UButton
          v-if="!wizardOnLastStep && !wizardShowsAll"
          color="primary"
          type="button"
          trailing-icon="i-lucide-arrow-right"
          data-testid="wizard-next"
          @click="wizardNext"
        >
          {{ t('common.next') }}
        </UButton>
        <UButton
          v-show="wizardOnLastStep || wizardShowsAll"
          color="primary"
          variant="solid"
          type="submit"
          form="create-account-form"
          :disabled="submitting"
          data-tour="account-form-submit" :loading="submitting"
        >
          {{
            isOAuthFlow
              ? t('common.next')
              : submitting
                ? t('admin.accounts.creating')
                : t('common.create')
          }}
        </UButton>
      </div>
      <div v-else class="flex justify-between gap-3">
        <UButton color="neutral" variant="outline" type="button" @click="goBackToBasicInfo">
          {{ t('common.back') }}
        </UButton>
        <UButton
          color="primary"
          variant="solid"
          v-if="isManualInputMethod"
          type="button"
          :disabled="!canExchangeCode"
          @click="handleExchangeCode" :loading="currentOAuthLoading"
        >
          {{
            currentOAuthLoading
              ? t('admin.accounts.oauth.verifying')
              : t('admin.accounts.oauth.completeAuth')
          }}
        </UButton>
      </div>
    </template>
  </BaseDialog>

  <!-- Gemini Help Dialog -->
  <BaseDialog
    :show="showGeminiHelpDialog"
    :title="t('admin.accounts.gemini.helpDialog.title')"
    width="wide"
    @close="showGeminiHelpDialog = false"
  >
    <div class="space-y-6">
      <!-- Setup Guide Section -->
      <div>
        <h3 class="mb-3 text-sm font-semibold text-highlighted">
          {{ t('admin.accounts.gemini.setupGuide.title') }}
        </h3>
        <div class="space-y-4">
          <div>
            <p class="mb-2 text-sm font-medium text-toned">
              {{ t('admin.accounts.gemini.setupGuide.checklistTitle') }}
            </p>
            <ul class="list-inside list-disc space-y-1 text-sm text-toned">
              <li>{{ t('admin.accounts.gemini.setupGuide.checklistItems.usIp') }}</li>
              <li>{{ t('admin.accounts.gemini.setupGuide.checklistItems.age') }}</li>
            </ul>
          </div>
          <div>
            <p class="mb-2 text-sm font-medium text-toned">
              {{ t('admin.accounts.gemini.setupGuide.activationTitle') }}
            </p>
            <ul class="list-inside list-disc space-y-1 text-sm text-toned">
              <li>{{ t('admin.accounts.gemini.setupGuide.activationItems.geminiWeb') }}</li>
              <li>{{ t('admin.accounts.gemini.setupGuide.activationItems.gcpProject') }}</li>
            </ul>
            <div class="mt-2 flex flex-wrap gap-2">
              <a
                href="https://policies.google.com/terms"
                target="_blank"
                rel="noreferrer"
                class="text-sm text-primary hover:underline"
              >
                {{ t('admin.accounts.gemini.setupGuide.links.countryCheck') }}
              </a>
              <span class="text-muted">·</span>
              <a
                href="https://policies.google.com/country-association-form"
                target="_blank"
                rel="noreferrer"
                class="text-sm text-primary hover:underline"
              >
                {{ t('admin.accounts.gemini.setupGuide.links.countryChange') }}
              </a>
              <span class="text-muted">·</span>
              <a
                href="https://gemini.google.com/gems/create?hl=en-US&pli=1"
                target="_blank"
                rel="noreferrer"
                class="text-sm text-primary hover:underline"
              >
                {{ t('admin.accounts.gemini.setupGuide.links.geminiWebActivation') }}
              </a>
              <span class="text-muted">·</span>
              <a
                href="https://console.cloud.google.com"
                target="_blank"
                rel="noreferrer"
                class="text-sm text-primary hover:underline"
              >
                {{ t('admin.accounts.gemini.setupGuide.links.gcpProject') }}
              </a>
            </div>
          </div>
        </div>
      </div>

      <!-- Quota Policy Section -->
      <div class="border-t border-default pt-6">
        <h3 class="mb-3 text-sm font-semibold text-highlighted">
          {{ t('admin.accounts.gemini.quotaPolicy.title') }}
        </h3>
        <p class="mb-4 text-xs text-warning">
          {{ t('admin.accounts.gemini.quotaPolicy.note') }}
        </p>
        <div class="overflow-x-auto">
          <UTable
            :data="geminiQuotaPolicyRows"
            :columns="geminiQuotaPolicyColumns"
            :ui="{ th: 'px-3 py-2 text-xs font-medium text-toned', td: 'px-3 py-2 text-xs text-toned whitespace-normal' }"
          />
        </div>
        <div class="mt-4 flex flex-wrap gap-3">
          <a
            :href="geminiQuotaDocs.codeAssist"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-primary hover:underline"
          >
            {{ t('admin.accounts.gemini.quotaPolicy.docs.codeAssist') }}
          </a>
          <a
            :href="geminiQuotaDocs.aiStudio"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-primary hover:underline"
          >
            {{ t('admin.accounts.gemini.quotaPolicy.docs.aiStudio') }}
          </a>
          <a
            :href="geminiQuotaDocs.vertex"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-primary hover:underline"
          >
            {{ t('admin.accounts.gemini.quotaPolicy.docs.vertex') }}
          </a>
        </div>
      </div>

      <!-- API Key Links Section -->
      <div class="border-t border-default pt-6">
        <h3 class="mb-3 text-sm font-semibold text-highlighted">
          {{ t('admin.accounts.gemini.helpDialog.apiKeySection') }}
        </h3>
        <div class="flex flex-wrap gap-3">
          <a
            :href="geminiHelpLinks.apiKey"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-primary hover:underline"
          >
            {{ t('admin.accounts.gemini.accountType.apiKeyLink') }}
          </a>
          <a
            :href="geminiHelpLinks.aiStudioPricing"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-primary hover:underline"
          >
            {{ t('admin.accounts.gemini.accountType.quotaLink') }}
          </a>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <UButton color="primary" variant="solid" @click="showGeminiHelpDialog = false" type="button">
          {{ t('common.close') }}
        </UButton>
      </div>
    </template>
  </BaseDialog>

  <!-- Mixed Channel Warning Dialog -->
  <ConfirmDialog
    :show="showMixedChannelWarning"
    :title="t('admin.accounts.mixedChannelWarningTitle')"
    :message="mixedChannelWarningMessageText"
    :confirm-text="t('common.confirm')"
    :cancel-text="t('common.cancel')"
    :danger="true"
    @confirm="handleMixedChannelConfirm"
    @cancel="handleMixedChannelCancel"
  />
</template>

<script setup lang="ts">
import '@/components/admin/page/admin-page.css'
import CopilotSDKFields from '@/components/account/CopilotSDKFields.vue'
import GeminiCacheAccountFields from '@/components/account/GeminiCacheAccountFields.vue'
import { readGeminiCacheSettings, applyGeminiCacheSettings } from '@/components/account/geminiCacheSettings'

import { ref, reactive, computed, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'

import {
  claudeModels,
  getPresetMappingsByPlatform,
  getModelsByPlatform,
  commonErrorCodes,
  buildModelMappingObject,
  fetchAntigravityDefaultMappings,
  mergeUpstreamIdentityMappings,
  isValidWildcardPattern
} from '@/composables/useModelWhitelist'
import { adminAPI } from '@/api/admin'
import { useQuotaNotifyState } from '@/composables/useQuotaNotifyState'
import {
  useAccountOAuth,
  type AddMethod,
  type AuthInputMethod
} from '@/composables/useAccountOAuth'
import { useOpenAIOAuth } from '@/composables/useOpenAIOAuth'
import { useGeminiOAuth } from '@/composables/useGeminiOAuth'
import { useAntigravityOAuth } from '@/composables/useAntigravityOAuth'
import { useGrokOAuth } from '@/composables/useGrokOAuth'
import type {
  Proxy,
  AdminGroup,
  AccountPlatform,
  AccountType,
  CheckMixedChannelResponse,
  CreateAccountRequest,
  CodexSessionImportMessage,
  OpenAICompactMode,
  OpenAIResponsesMode,
  OpenAIEndpointCapability
} from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import UpstreamRequestIdHeaderField from '@/components/account/UpstreamRequestIdHeaderField.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import ProxySelector from '@/components/common/ProxySelector.vue'
import ProxyAdBanner from '@/components/common/ProxyAdBanner.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import ModelWhitelistSelector from '@/components/account/ModelWhitelistSelector.vue'
import QuotaLimitCard from '@/components/account/QuotaLimitCard.vue'
import Toggle from '@/components/common/Toggle.vue'
import GrokBaseUrlPresets from '@/components/account/GrokBaseUrlPresets.vue'
import CnBaseUrlPresets from '@/components/account/CnBaseUrlPresets.vue'
import HeaderOverrideEditor from '@/components/account/HeaderOverrideEditor.vue'
import { allSelectedGroupsEnableLongContextPricing } from '@/components/account/longContextBilling'
import {
  applyAntigravityProjectID,
  applyHeaderOverride,
  applyInterceptWarmup,
  cnSupportsNativeResponses,
  defaultCNAdaptiveBaseUrls,
  defaultCNBaseUrl,
  isAdaptiveProtocolPlatform,
  isCNProviderPlatform,
  isHeaderOverrideCapable,
  validateHeaderOverrideRows,
  type AdaptiveProtocolPlatform,
  type CnAccountMode,
  type CnApiProtocol,
  type CnNativeApiProtocol,
  type CnProviderPlatform,
  type HeaderOverrideRow
} from '@/components/account/credentialsBuilder'
import {
  formatDateTimeLocalInput,
  getBrowserTimeZone,
  parseDateTimeLocalInput
} from '@/utils/format'
import { createStableObjectKeyResolver } from '@/utils/stableObjectKey'
import { getAccountExpiryTimestamp } from '@/components/account/accountExpiry'
import { VERTEX_LOCATION_OPTIONS } from '@/constants/account'
import {
  OPENAI_WS_MODE_CTX_POOL,
  OPENAI_WS_MODE_OFF,
  OPENAI_WS_MODE_PASSTHROUGH,
  OPENAI_WS_MODE_HTTP_BRIDGE,
  isOpenAIWSModeEnabled,
  resolveOpenAIWSModeConcurrencyHintKey,
  type OpenAIWSMode
} from '@/utils/openaiWsMode'
import OAuthAuthorizationFlow from './OAuthAuthorizationFlow.vue'
import { getActivePinia } from 'pinia'
import { useOnboardingStore } from '@/stores/onboarding'
import { confirm } from '@/composables/useConfirmation'

// Compact selectable UPageCard (platform / type pickers): selection shows as the primary ring.
const choiceCardUi = { root: 'cursor-pointer text-start hover:bg-elevated/50 focus-visible:outline-2 focus-visible:outline-primary', container: 'p-3 sm:p-3 gap-0 lg:flex', wrapper: 'flex-row items-center gap-3', leading: 'mb-0', leadingIcon: 'size-5 text-muted', title: 'text-sm font-medium', description: 'mt-0.5 text-xs text-muted' }
// Gemini help dialog: quota policy per channel (channel shown on the first row of each group).
const geminiQuotaPolicyColumns = computed(() => [
  { accessorKey: 'channel', header: t('admin.accounts.gemini.quotaPolicy.columns.channel'), meta: { class: { td: 'font-medium text-highlighted' } } },
  { accessorKey: 'account', header: t('admin.accounts.gemini.quotaPolicy.columns.account') },
  { accessorKey: 'limits', header: t('admin.accounts.gemini.quotaPolicy.columns.limits') }
])
const geminiQuotaPolicyRows = computed(() => [
  { channel: t('admin.accounts.gemini.quotaPolicy.rows.googleOne.channel'), account: 'Free', limits: t('admin.accounts.gemini.quotaPolicy.rows.googleOne.limitsFree') },
  { channel: '', account: 'Pro', limits: t('admin.accounts.gemini.quotaPolicy.rows.googleOne.limitsPro') },
  { channel: '', account: 'Ultra', limits: t('admin.accounts.gemini.quotaPolicy.rows.googleOne.limitsUltra') },
  { channel: t('admin.accounts.gemini.quotaPolicy.rows.gcp.channel'), account: 'Standard', limits: t('admin.accounts.gemini.quotaPolicy.rows.gcp.limitsStandard') },
  { channel: '', account: 'Enterprise', limits: t('admin.accounts.gemini.quotaPolicy.rows.gcp.limitsEnterprise') },
  { channel: t('admin.accounts.gemini.quotaPolicy.rows.aiStudio.channel'), account: 'Free', limits: t('admin.accounts.gemini.quotaPolicy.rows.aiStudio.limitsFree') },
  { channel: '', account: 'Paid', limits: t('admin.accounts.gemini.quotaPolicy.rows.aiStudio.limitsPaid') }
])

// Type for exposed OAuthAuthorizationFlow component
// Note: defineExpose automatically unwraps refs, so we use the unwrapped types
interface OAuthFlowExposed {
  authCode: string
  oauthState: string
  projectId: string
  sessionKey: string
  refreshToken: string
  sessionToken: string
  codexSession: string
  codexPAT: string
  ssoCookie: string
  inputMethod: AuthInputMethod
  reset: () => void
}

const { t } = useI18n()
const browserTimeZone = getBrowserTimeZone()

const oauthStepTitle = computed(() => {
  if (form.platform === 'openai') return t('admin.accounts.oauth.openai.title')
  if (form.platform === 'gemini') return t('admin.accounts.oauth.gemini.title')
  if (form.platform === 'antigravity') return t('admin.accounts.oauth.antigravity.title')
  if (form.platform === 'grok') return t('admin.accounts.oauth.grok.title')
  return t('admin.accounts.oauth.title')
})

// Platform-specific hints for API Key type
// 上游ID：直接上游声明请求标识的响应头名，留空不记录。
const upstreamRequestIdHeader = ref('')
const withUpstreamRequestIdHeader = <T extends Record<string, unknown> | undefined>(extra: T): T | Record<string, unknown> => {
  const name = upstreamRequestIdHeader.value.trim()
  if (!name) return extra
  return { ...(extra || {}), upstream_request_id_header: name }
}

const baseUrlHint = computed(() => {
  if (isGeminiProtocolAccount.value) return t('admin.accounts.gemini.accountType.customProtocolHint')
  if (form.platform === 'openai') return t('admin.accounts.openai.baseUrlHint')
  if (form.platform === 'gemini') return t('admin.accounts.gemini.baseUrlHint')
  if (form.platform === 'grok') return ''
  return t('admin.accounts.baseUrlHint')
})

const apiKeyHint = computed(() => {
  if (form.type === 'cloudflare') return ''
  if (isGeminiProtocolAccount.value) return t('admin.accounts.openai.apiKeyHint')
  if (form.platform === 'openai') return t('admin.accounts.openai.apiKeyHint')
  if (form.platform === 'gemini') return t('admin.accounts.gemini.apiKeyHint')
  if (form.platform === 'grok') return ''
  return t('admin.accounts.apiKeyHint')
})

// Base URL / API Key 占位符：国产供应商随账号类型变化。
const apiKeyBaseUrlPlaceholder = computed(() => {
  if (isAdaptiveProtocolAccount.value) {
    return defaultCNBaseUrl(form.platform, accountMode.value, apiProtocol.value) || 'https://your-upstream/v1'
  }
  switch (form.platform) {
    case 'openai':
      return 'https://api.openai.com'
    case 'gemini':
      return 'https://generativelanguage.googleapis.com'
    case 'grok':
      return 'https://api.x.ai/v1'
    case 'typesafe':
      return 'https://api.typesafe.ai'
    default:
      return 'https://api.anthropic.com'
  }
})

const apiKeyValuePlaceholder = computed(() => {
  switch (form.platform) {
    case 'openai':
      return 'sk-proj-...'
    case 'gemini':
      return 'AIza...'
    case 'grok':
      return 'xai-...'
    case 'kimi':
      return 'sk-...'
    case 'zhipu':
      return '<api-key>.<secret>'
    case 'deepseek':
      return 'sk-...'
    case 'minimax':
      return 'sk-...'
    case 'typesafe':
      return 'ts-...'
    default:
      return 'sk-ant-...'
  }
})

interface Props {
  show: boolean
  proxies: Proxy[]
  groups: AdminGroup[]
}

const props = defineProps<Props>()
const emit = defineEmits<{
  close: []
  created: []
}>()

const appStore = useAppStore()

const hideAccountLongContextBilling = computed(() => {
  return allSelectedGroupsEnableLongContextPricing(form.group_ids, props.groups)
})

// OAuth composables
const oauth = useAccountOAuth() // For Anthropic OAuth
const openaiOAuth = useOpenAIOAuth() // For OpenAI OAuth
const geminiOAuth = useGeminiOAuth() // For Gemini OAuth
const antigravityOAuth = useAntigravityOAuth() // For Antigravity OAuth
const grokOAuth = useGrokOAuth() // For Grok OAuth

// Computed: current OAuth state for template binding
const currentAuthUrl = computed(() => {
  if (form.platform === 'openai') return openaiOAuth.authUrl.value
  if (form.platform === 'gemini') return geminiOAuth.authUrl.value
  if (form.platform === 'antigravity') return antigravityOAuth.authUrl.value
  if (form.platform === 'grok') return grokOAuth.authUrl.value
  return oauth.authUrl.value
})

const currentSessionId = computed(() => {
  if (form.platform === 'openai') return openaiOAuth.sessionId.value
  if (form.platform === 'gemini') return geminiOAuth.sessionId.value
  if (form.platform === 'antigravity') return antigravityOAuth.sessionId.value
  if (form.platform === 'grok') return grokOAuth.sessionId.value
  return oauth.sessionId.value
})

const currentOAuthLoading = computed(() => {
  if (form.platform === 'openai') return openaiOAuth.loading.value
  if (form.platform === 'gemini') return geminiOAuth.loading.value
  if (form.platform === 'antigravity') return antigravityOAuth.loading.value
  if (form.platform === 'grok') return grokOAuth.loading.value
  return oauth.loading.value
})

const currentOAuthError = computed(() => {
  if (form.platform === 'openai') return openaiOAuth.error.value
  if (form.platform === 'gemini') return geminiOAuth.error.value
  if (form.platform === 'antigravity') return antigravityOAuth.error.value
  if (form.platform === 'grok') return grokOAuth.error.value
  return oauth.error.value
})

// Refs
const oauthFlowRef = ref<OAuthFlowExposed | null>(null)

// Model mapping type
interface ModelMapping {
  from: string
  to: string
}

interface TempUnschedRuleForm {
  error_code: number | null
  keywords: string
  duration_minutes: number | null
  description: string
}

// State
const step = ref(1)
const submitting = ref(false)
const accountCategory = ref<'oauth-based' | 'apikey' | 'cloudflare' | 'bedrock' | 'service_account'>('oauth-based') // UI selection for account category
const addMethod = ref<AddMethod>('oauth') // For oauth-based: 'oauth' or 'setup-token'
const apiKeyBaseUrl = ref('https://api.anthropic.com')
const apiKeyValue = ref('')
const cloudflareAccountID = ref('')
const upstreamBillingAutoProbeEnabled = ref(true)

// ── 国产供应商（Kimi / Zhipu / DeepSeek）账号类型、API 协议与端点 ──
const accountMode = ref<CnAccountMode>('payg')
// API 协议决定转发端点与格式：cc=现有转换链，anthropic=原生直通（Claude Code），
// responses=deepseek / kimi 原生 Responses 端点（Codex）。与账号类型正交。
const apiProtocol = ref<CnApiProtocol>('adaptive')
// 智谱团队版 Coding Plan：组织/项目 ID，写入 credentials 供额度探测切换团队端点
const zhipuOrganization = ref('')
const zhipuProject = ref('')
const adaptiveBaseUrls = ref<Record<CnNativeApiProtocol, string>>({
  chat_completions: '',
  anthropic: '',
  responses: ''
})
const copilotSDKEnabled = ref(false)
const geminiUseCustomProtocol = ref(false)
const geminiCacheSettings = ref(readGeminiCacheSettings())
const isCNPlatform = computed(() => isCNProviderPlatform(form.platform))
const isGeminiProtocolAccount = computed(
  () => form.platform === 'gemini' && accountCategory.value === 'apikey' && geminiUseCustomProtocol.value
)
const isAdaptiveProtocolAccount = computed(
  () => isCNPlatform.value || isGeminiProtocolAccount.value
)
// CnBaseUrlPresets 的 platform prop 是平台字面量联合类型，模板里不能写
// `as` 断言（其中的 `|` 会被 eslint 误判为 Vue2 filter 语法），经此 computed 传递。
const cnPresetPlatform = computed<CnProviderPlatform>(() => {
  if (isCNProviderPlatform(form.platform)) {
    return form.platform
  }
  return 'kimi'
})
// Protocol options: responses is DeepSeek / Kimi / MiniMax / Gemini custom upstream.
const cnProtocolOptions = computed<Array<{ value: CnApiProtocol; labelKey: string }>>(() => {
  const opts: Array<{ value: CnApiProtocol; labelKey: string }> = [
    { value: 'adaptive', labelKey: 'adaptive' },
    { value: 'chat_completions', labelKey: 'chatCompletions' },
    { value: 'anthropic', labelKey: 'anthropic' }
  ]
  if (cnSupportsNativeResponses(form.platform)) {
    opts.push({ value: 'responses', labelKey: 'responses' })
  }
  return opts
})
const adaptiveProtocolEndpointOptions = computed<Array<{ value: CnNativeApiProtocol; labelKey: string }>>(() => {
  const opts: Array<{ value: CnNativeApiProtocol; labelKey: string }> = [
    { value: 'chat_completions', labelKey: 'chatCompletions' },
    { value: 'anthropic', labelKey: 'anthropic' }
  ]
  if (cnSupportsNativeResponses(form.platform)) opts.push({ value: 'responses', labelKey: 'responses' })
  return opts
})

function resetAdaptiveBaseUrls(platform: AdaptiveProtocolPlatform, mode: CnAccountMode) {
  adaptiveBaseUrls.value = defaultCNAdaptiveBaseUrls(platform, mode)
}
// 切换国产供应商平台：强制 apikey 类型，deepseek 无 coding 套餐故锁定 payg，
// 协议回落 adaptive，并把 base url 重置为该平台默认端点。
function selectCNPlatform(platform: CnProviderPlatform) {
  form.platform = platform
  form.type = 'apikey'
  accountCategory.value = 'apikey'
  apiProtocol.value = 'adaptive'
  if (platform === 'deepseek') {
    accountMode.value = 'payg'
  }
  apiKeyBaseUrl.value = defaultCNBaseUrl(platform, accountMode.value, apiProtocol.value)
  resetAdaptiveBaseUrls(platform, accountMode.value)
}
// TypeSafe 仅支持 apikey + 原生 System One 协议，模型固定 jev-latest。
function selectTypeSafePlatform() {
  form.platform = 'typesafe'
  form.type = 'apikey'
  accountCategory.value = 'apikey'
  apiKeyBaseUrl.value = 'https://api.typesafe.ai'
  allowedModels.value = ['jev-latest']
}
// 账号类型 / 协议变更时同步默认 base url。
watch(accountMode, (mode, previousMode) => {
  if (!isAdaptiveProtocolAccount.value || !isAdaptiveProtocolPlatform(form.platform)) return
  if (apiProtocol.value === 'adaptive') {
    const previousDefaults = defaultCNAdaptiveBaseUrls(form.platform, previousMode)
    const nextDefaults = defaultCNAdaptiveBaseUrls(form.platform, mode)
    for (const item of adaptiveProtocolEndpointOptions.value) {
      if (!adaptiveBaseUrls.value[item.value] || adaptiveBaseUrls.value[item.value] === previousDefaults[item.value]) {
        adaptiveBaseUrls.value[item.value] = nextDefaults[item.value]
      }
    }
    apiKeyBaseUrl.value = adaptiveBaseUrls.value.chat_completions
    return
  }
  apiKeyBaseUrl.value = defaultCNBaseUrl(form.platform, mode, apiProtocol.value)
})
watch(apiProtocol, (protocol) => {
  if (!isAdaptiveProtocolAccount.value || !isAdaptiveProtocolPlatform(form.platform)) return
  if (protocol === 'adaptive') {
    const defaults = defaultCNAdaptiveBaseUrls(form.platform, accountMode.value)
    for (const item of adaptiveProtocolEndpointOptions.value) {
      if (!adaptiveBaseUrls.value[item.value]) adaptiveBaseUrls.value[item.value] = defaults[item.value]
    }
    apiKeyBaseUrl.value = adaptiveBaseUrls.value.chat_completions
    return
  }
  apiKeyBaseUrl.value = defaultCNBaseUrl(form.platform, accountMode.value, protocol)
})
watch(geminiUseCustomProtocol, (enabled) => {
  if (form.platform !== 'gemini' || accountCategory.value !== 'apikey') return
  if (enabled) {
    apiProtocol.value = 'adaptive'
    resetAdaptiveBaseUrls('gemini', 'payg')
    apiKeyBaseUrl.value = adaptiveBaseUrls.value.chat_completions || adaptiveBaseUrls.value.responses || 'https://your-upstream/v1'
    return
  }
  apiKeyBaseUrl.value = 'https://generativelanguage.googleapis.com'
})
// 点击预设端点：同时回填 base url、账号类型与协议。
function onCnPresetSelect(preset: { mode: CnAccountMode; protocol: CnApiProtocol; url: string }) {
  accountMode.value = preset.mode
  apiProtocol.value = preset.protocol
  apiKeyBaseUrl.value = preset.url
}

const syncPreviewCredentials = computed(() => {
  if (!apiKeyValue.value) return undefined
  const baseUrl = isAdaptiveProtocolAccount.value && apiProtocol.value === 'adaptive'
    ? adaptiveBaseUrls.value.chat_completions.trim() || apiKeyBaseUrl.value.trim()
    : apiKeyBaseUrl.value.trim()
  const modelMapping = buildModelMappingObject(
    modelRestrictionMode.value,
    allowedModels.value,
    modelMappings.value
  )
  const preview: {
    platform: string
    type: string
    base_url?: string
    account_id?: string
    api_key: string
    api_protocol?: string
    api_base_urls?: Record<string, string>
    model_mapping?: Record<string, string>
  } = {
    platform: form.platform,
    type: form.type,
    api_key: apiKeyValue.value,
    ...(modelMapping ? { model_mapping: modelMapping } : {})
  }
  if (form.type === 'cloudflare') {
    preview.account_id = cloudflareAccountID.value.trim()
  } else {
    preview.base_url = baseUrl || undefined
  }
  if (form.platform === 'gemini' && accountCategory.value === 'apikey') {
    const customBase = (baseUrl || '').toLowerCase()
    const isOfficialGoogle = customBase.includes('generativelanguage.googleapis.com')
    if (isGeminiProtocolAccount.value || (customBase && !isOfficialGoogle)) {
      preview.api_protocol = isGeminiProtocolAccount.value ? apiProtocol.value : 'chat_completions'
      if (isGeminiProtocolAccount.value && apiProtocol.value === 'adaptive') {
        preview.api_base_urls = {
          chat_completions: adaptiveBaseUrls.value.chat_completions.trim() || baseUrl,
          anthropic: adaptiveBaseUrls.value.anthropic.trim(),
          responses: adaptiveBaseUrls.value.responses.trim()
        }
      }
    }
  }
  return preview
})

const editQuotaLimit = ref<number | null>(null)
const editQuotaDailyLimit = ref<number | null>(null)
const editQuotaWeeklyLimit = ref<number | null>(null)
const editDailyResetMode = ref<'rolling' | 'fixed' | null>(null)
const editDailyResetHour = ref<number | null>(null)
const editWeeklyResetMode = ref<'rolling' | 'fixed' | null>(null)
const editWeeklyResetDay = ref<number | null>(null)
const editWeeklyResetHour = ref<number | null>(null)
const editResetTimezone = ref<string | null>(null)
const modelMappings = ref<ModelMapping[]>([])
const modelMappingFromKeys = computed(() =>
  modelMappings.value.map((mapping) => mapping.from).filter(Boolean)
)
const openAICompactModelMappings = ref<ModelMapping[]>([])
const modelRestrictionMode = ref<'whitelist' | 'mapping'>('whitelist')
const allowedModels = ref<string[]>([])
const upstreamModelsPreviewed = ref(false)
const DEFAULT_POOL_MODE_RETRY_COUNT = 3
const MAX_POOL_MODE_RETRY_COUNT = 10
const DEFAULT_POOL_MODE_RETRY_STATUS_CODES = [401, 403, 429]
const poolModeEnabled = ref(false)
const poolModeRetryCount = ref(DEFAULT_POOL_MODE_RETRY_COUNT)
const poolModeRetryStatusCodesInput = ref('')

function parsePoolModeRetryStatusCodes(input: string): number[] {
  if (!input || !input.trim()) return []
  const seen = new Set<number>()
  const out: number[] = []
  for (const token of input.split(/[,\s]+/)) {
    const trimmed = token.trim()
    if (!trimmed) continue
    const n = Number(trimmed)
    if (!Number.isFinite(n) || !Number.isInteger(n)) continue
    if (n < 100 || n > 599) continue
    if (seen.has(n)) continue
    seen.add(n)
    out.push(n)
  }
  return out.sort((a, b) => a - b)
}
const customErrorCodesEnabled = ref(false)
const selectedErrorCodes = ref<number[]>([])
const customErrorCodeInput = ref<number | null>(null)
const headerOverrideEnabled = ref(false)
const headerOverrideRows = ref<HeaderOverrideRow[]>([])

// Grok OAuth：自定义上游地址（base_url 仅改写转发端点，OAuth 授权/刷新不受影响）
const grokOAuthCustomBaseUrlEnabled = ref(false)
const grokOAuthBaseUrl = ref('')

// Grok OAuth 三条创建路径（授权码/RT 批量/SSO 批量）共用的前置校验。
// 授权码路径必须在兑换 code 之前调用，避免校验失败时白白消耗一次性授权码。
const validateGrokOAuthUpstreamConfig = (): boolean => {
  if (grokOAuthCustomBaseUrlEnabled.value) {
    const trimmed = grokOAuthBaseUrl.value.trim()
    if (!trimmed) {
      appStore.showError(t('admin.accounts.grokCustomBaseUrl.required'))
      return false
    }
    if (!/^https?:\/\//i.test(trimmed)) {
      appStore.showError(t('admin.accounts.grokCustomBaseUrl.invalid'))
      return false
    }
  }
  if (headerOverrideEnabled.value) {
    const headerError = validateHeaderOverrideRows(headerOverrideRows.value)
    if (headerError) {
      appStore.showError(t(`admin.accounts.headerOverride.${headerError}`))
      return false
    }
  }
  return true
}

// 把已通过校验的自定义上游地址与请求头覆写写入 credentials
const applyGrokOAuthUpstreamConfig = (credentials: Record<string, unknown>) => {
  if (grokOAuthCustomBaseUrlEnabled.value) {
    credentials.base_url = grokOAuthBaseUrl.value.trim()
  }
  applyHeaderOverride(credentials, headerOverrideEnabled.value, headerOverrideRows.value, 'create')
}
const interceptWarmupRequests = ref(false)
const autoPauseOnExpired = ref(true)
const openaiPassthroughEnabled = ref(false)
// OpenAI Codex namespace 工具摊平兼容开关（仅 OAuth），缺省关闭即原样保留
const openaiFlattenNamespacesEnabled = ref(false)
const openAILongContextBillingEnabled = ref(false)
const openAILongContextBillingTouched = ref(false)
const openAICompactMode = ref<OpenAICompactMode>('auto')
const openAIResponsesMode = ref<OpenAIResponsesMode>('auto')
// Images 非流式响应缺 b64_json 时由网关下载 url 回填（仅 OpenAI API Key）。
const openAIImagesUrlToB64JsonEnabled = ref(false)
const openAIEndpointCapabilities = ref<OpenAIEndpointCapability[]>(['chat_completions', 'embeddings'])
const openaiOAuthResponsesWebSocketV2Mode = ref<OpenAIWSMode>(OPENAI_WS_MODE_OFF)
const openaiAPIKeyResponsesWebSocketV2Mode = ref<OpenAIWSMode>(OPENAI_WS_MODE_OFF)
const codexCLIOnlyEnabled = ref(false)
const codexCLIOnlyAppServerEnabled = ref(false)
type CodexFingerprintMode = 'off' | 'device' | 'session' | 'full'
const codexFingerprintMode = ref<CodexFingerprintMode>('off')
const codexFingerprintModeOptions = computed(() => [
  { value: 'off' as CodexFingerprintMode, label: t('admin.accounts.openai.codexFingerprintOff') },
  { value: 'device' as CodexFingerprintMode, label: t('admin.accounts.openai.codexFingerprintDevice') },
  { value: 'session' as CodexFingerprintMode, label: t('admin.accounts.openai.codexFingerprintSession') },
  { value: 'full' as CodexFingerprintMode, label: t('admin.accounts.openai.codexFingerprintFull') },
])
type AnthropicAPIKeyAuthScheme = 'x_api_key' | 'authorization_bearer'
const anthropicPassthroughEnabled = ref(false)
const anthropicAPIKeyAuthScheme = ref<AnthropicAPIKeyAuthScheme>('x_api_key')
const webSearchEmulationMode = ref('default')
const webSearchGlobalEnabled = ref(false)

const toggleOpenAILongContextBilling = () => {
  openAILongContextBillingEnabled.value = !openAILongContextBillingEnabled.value
  openAILongContextBillingTouched.value = true
}
const {
  globalEnabled: quotaNotifyGlobalEnabled,
  state: quotaNotifyState,
  loadGlobalState: loadQuotaNotifyGlobal,
  writeToExtra: writeQuotaNotifyToExtra,
} = useQuotaNotifyState()

// Load global feature states once
adminAPI.settings.getWebSearchEmulationConfig().then(cfg => {
  webSearchGlobalEnabled.value = cfg?.enabled === true && (cfg?.providers?.length ?? 0) > 0
}).catch(() => { webSearchGlobalEnabled.value = false })

loadQuotaNotifyGlobal()
const mixedScheduling = ref(false) // For antigravity accounts: enable mixed scheduling
const allowOverages = ref(false) // For antigravity accounts: enable AI Credits overages
const antigravityAccountType = ref<'oauth' | 'upstream'>('oauth') // For antigravity: oauth or upstream
const antigravityProjectId = ref('')
const upstreamBaseUrl = ref('') // For upstream type: base URL
const upstreamApiKey = ref('') // For upstream type: API key
const antigravityModelRestrictionMode = ref<'whitelist' | 'mapping'>('whitelist')
const antigravityWhitelistModels = ref<string[]>([])
const antigravityModelMappings = ref<ModelMapping[]>([])
const antigravityMappingFromKeys = computed(() =>
  antigravityModelMappings.value.map((mapping) => mapping.from).filter(Boolean)
)
const antigravityPresetMappings = computed(() => getPresetMappingsByPlatform('antigravity'))
const bedrockPresets = computed(() => getPresetMappingsByPlatform('bedrock'))

// Bedrock credentials
const bedrockAuthMode = ref<'sigv4' | 'apikey'>('sigv4')
const bedrockAccessKeyId = ref('')
const bedrockSecretAccessKey = ref('')
const bedrockSessionToken = ref('')
const bedrockRegion = ref('us-east-1')
const bedrockForceGlobal = ref(false)
const bedrockApiKeyValue = ref('')
const vertexServiceAccountFileInput = ref<HTMLInputElement | null>(null)
const vertexServiceAccountJson = ref('')
const vertexProjectId = ref('')
const vertexClientEmail = ref('')
const vertexLocation = ref('global')
const vertexServiceAccountDragActive = ref(false)
const tempUnschedEnabled = ref(false)
const tempUnschedRules = ref<TempUnschedRuleForm[]>([])
const getModelMappingKey = createStableObjectKeyResolver<ModelMapping>('create-model-mapping')
const getOpenAICompactModelMappingKey = createStableObjectKeyResolver<ModelMapping>('create-openai-compact-model-mapping')
const getAntigravityModelMappingKey = createStableObjectKeyResolver<ModelMapping>('create-antigravity-model-mapping')
const getTempUnschedRuleKey = createStableObjectKeyResolver<TempUnschedRuleForm>('create-temp-unsched-rule')
const geminiOAuthType = ref<'code_assist' | 'google_one' | 'ai_studio'>('google_one')
const geminiAIStudioOAuthEnabled = ref(false)
const openAICompactModeOptions = computed(() => [
  { value: 'auto', label: t('admin.accounts.openai.compactModeAuto') },
  { value: 'force_on', label: t('admin.accounts.openai.compactModeForceOn') },
  { value: 'force_off', label: t('admin.accounts.openai.compactModeForceOff') }
])
const openAIResponsesModeOptions = computed(() => [
  { value: 'auto', label: t('admin.accounts.openai.responsesModeAuto') },
  { value: 'force_responses', label: t('admin.accounts.openai.responsesModeForceResponses') },
  { value: 'force_chat_completions', label: t('admin.accounts.openai.responsesModeForceChatCompletions') }
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
const openAIEndpointCapabilityOptions = computed<{ value: OpenAIEndpointCapability; label: string }[]>(() => [
  { value: 'chat_completions', label: openAITextEndpointCapabilityLabel.value },
  { value: 'embeddings', label: t('admin.accounts.openai.capabilityEmbeddings') },
  { value: 'seedance', label: 'Seedance / Ark video' }
])
const openAITextGenerationCapabilityEnabled = computed(() =>
  openAIEndpointCapabilities.value.includes('chat_completions')
)

const normalizeOpenAIEndpointCapabilities = (values: OpenAIEndpointCapability[]): OpenAIEndpointCapability[] => {
  const allowed: OpenAIEndpointCapability[] = ['chat_completions', 'embeddings', 'seedance']
  const selected = allowed.filter((value) => values.includes(value))
  return selected.length > 0 ? selected : ['chat_completions', 'embeddings']
}

const toggleOpenAIEndpointCapability = (capability: OpenAIEndpointCapability, event?: Event) => {
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

const applyOpenAIEndpointCapabilities = (credentials: Record<string, unknown>) => {
  const capabilities = normalizeOpenAIEndpointCapabilities(openAIEndpointCapabilities.value)
  if (capabilities.length === 2) {
    delete credentials.openai_capabilities
    return
  }
  credentials.openai_capabilities = capabilities
}

function buildAntigravityExtra(): Record<string, unknown> | undefined {
  const extra: Record<string, unknown> = {}
  if (mixedScheduling.value) extra.mixed_scheduling = true
  if (allowOverages.value) extra.allow_overages = true
  return Object.keys(extra).length > 0 ? extra : undefined
}

const buildOpenAICompactModelMapping = () =>
  buildModelMappingObject('mapping', [], openAICompactModelMappings.value)

const showMixedChannelWarning = ref(false)
const mixedChannelWarningDetails = ref<{ groupName: string; currentPlatform: string; otherPlatform: string } | null>(
  null
)
const mixedChannelWarningRawMessage = ref('')
const mixedChannelWarningAction = ref<(() => Promise<void>) | null>(null)
const antigravityMixedChannelConfirmed = ref(false)
const showAdvancedOAuth = ref(false)
const showGeminiHelpDialog = ref(false)

// Quota control state (Anthropic OAuth/SetupToken only)
const windowCostEnabled = ref(false)
const windowCostLimit = ref<number | null>(null)
const windowCostStickyReserve = ref<number | null>(null)
const sessionLimitEnabled = ref(false)
const maxSessions = ref<number | null>(null)
const sessionIdleTimeout = ref<number | null>(null)
const rpmLimitEnabled = ref(false)
const baseRpm = ref<number | null>(null)
const rpmStrategy = ref<'tiered' | 'sticky_exempt'>('tiered')
const rpmStickyBuffer = ref<number | null>(null)
const userMsgQueueMode = ref('')
const umqModeOptions = computed(() => [
  { value: '', label: t('admin.accounts.quotaControl.rpmLimit.umqModeOff') },
  { value: 'throttle', label: t('admin.accounts.quotaControl.rpmLimit.umqModeThrottle') },
  { value: 'serialize', label: t('admin.accounts.quotaControl.rpmLimit.umqModeSerialize') },
])
const tlsFingerprintEnabled = ref(false)
const tlsFingerprintProfileId = ref<number | null>(null)
const tlsFingerprintProfiles = ref<{ id: number; name: string }[]>([])
const sessionIdMaskingEnabled = ref(false)
const cacheTTLOverrideEnabled = ref(false)
const cacheTTLOverrideTarget = ref<string>('5m')
const customBaseUrlEnabled = ref(false)
const customBaseUrl = ref('')

// Gemini tier selection (used as fallback when auto-detection is unavailable/fails)
const geminiTierGoogleOne = ref<'google_one_free' | 'google_ai_pro' | 'google_ai_ultra'>('google_one_free')
const geminiTierGcp = ref<'gcp_standard' | 'gcp_enterprise'>('gcp_standard')
const geminiTierAIStudio = ref<'aistudio_free' | 'aistudio_paid'>('aistudio_free')

const geminiSelectedTier = computed(() => {
  if (form.platform !== 'gemini') return ''
  if (accountCategory.value === 'apikey') return geminiTierAIStudio.value
  switch (geminiOAuthType.value) {
    case 'google_one':
      return geminiTierGoogleOne.value
    case 'code_assist':
      return geminiTierGcp.value
    default:
      return geminiTierAIStudio.value
  }
})

const openAIWSModeOptions = computed(() => [
  { value: OPENAI_WS_MODE_OFF, label: t('admin.accounts.openai.wsModeOff') },
  { value: OPENAI_WS_MODE_CTX_POOL, label: t('admin.accounts.openai.wsModeCtxPool') },
  { value: OPENAI_WS_MODE_PASSTHROUGH, label: t('admin.accounts.openai.wsModePassthrough') },
  { value: OPENAI_WS_MODE_HTTP_BRIDGE, label: t('admin.accounts.openai.wsModeHttpBridge') }
])

const openaiResponsesWebSocketV2Mode = computed({
  get: () => {
    if (form.platform === 'openai' && accountCategory.value === 'apikey') {
      return openaiAPIKeyResponsesWebSocketV2Mode.value
    }
    return openaiOAuthResponsesWebSocketV2Mode.value
  },
  set: (mode: OpenAIWSMode) => {
    if (form.platform === 'openai' && accountCategory.value === 'apikey') {
      openaiAPIKeyResponsesWebSocketV2Mode.value = mode
      return
    }
    openaiOAuthResponsesWebSocketV2Mode.value = mode
  }
})

const openAIWSModeConcurrencyHintKey = computed(() =>
  resolveOpenAIWSModeConcurrencyHintKey(openaiResponsesWebSocketV2Mode.value)
)

const isOpenAIModelRestrictionDisabled = computed(() =>
  form.platform === 'openai' && openaiPassthroughEnabled.value
)

const mixedChannelWarningMessageText = computed(() => {
  if (mixedChannelWarningDetails.value) {
    return t('admin.accounts.mixedChannelWarning', mixedChannelWarningDetails.value)
  }
  return mixedChannelWarningRawMessage.value
})

const geminiQuotaDocs = {
  codeAssist: 'https://developers.google.com/gemini-code-assist/resources/quotas',
  aiStudio: 'https://ai.google.dev/pricing',
  vertex: 'https://cloud.google.com/vertex-ai/generative-ai/docs/quotas'
}

const geminiHelpLinks = {
  apiKey: 'https://aistudio.google.com/app/apikey',
  aiStudioPricing: 'https://ai.google.dev/pricing',
  gcpProject: 'https://console.cloud.google.com/welcome/new',
  geminiWebActivation: 'https://gemini.google.com/gems/create?hl=en-US&pli=1',
  countryCheck: 'https://policies.google.com/terms',
  countryChange: 'https://policies.google.com/country-association-form'
}

// Computed: current preset mappings based on platform
const presetMappings = computed(() => getPresetMappingsByPlatform(form.platform))
const tempUnschedPresets = computed(() => [
  {
    label: t('admin.accounts.tempUnschedulable.presets.overloadLabel'),
    rule: {
      error_code: 529,
      keywords: 'overloaded, too many',
      duration_minutes: 60,
      description: t('admin.accounts.tempUnschedulable.presets.overloadDesc')
    }
  },
  {
    label: t('admin.accounts.tempUnschedulable.presets.rateLimitLabel'),
    rule: {
      error_code: 429,
      keywords: 'rate limit, too many requests',
      duration_minutes: 10,
      description: t('admin.accounts.tempUnschedulable.presets.rateLimitDesc')
    }
  },
  {
    label: t('admin.accounts.tempUnschedulable.presets.unavailableLabel'),
    rule: {
      error_code: 503,
      keywords: 'unavailable, maintenance',
      duration_minutes: 30,
      description: t('admin.accounts.tempUnschedulable.presets.unavailableDesc')
    }
  }
])

const form = reactive({
  name: '',
  notes: '',
  platform: 'anthropic' as AccountPlatform,
  type: 'oauth' as AccountType, // Will be 'oauth', 'setup-token', or 'apikey'
  credentials: {} as Record<string, unknown>,
  proxy_id: null as number | null,
  concurrency: 10,
  load_factor: null as number | null,
  priority: 1,
  rate_multiplier: 1,
  group_ids: [] as number[],
  expires_at: null as number | null
})

// Helper to check if current type needs OAuth flow
const isOAuthFlow = computed(() => {
  // Antigravity upstream 类型不需要 OAuth 流程
  if (form.platform === 'antigravity' && antigravityAccountType.value === 'upstream') {
    return false
  }
  // Bedrock 类型不需要 OAuth 流程
  if (form.platform === 'anthropic' && accountCategory.value === 'bedrock') {
    return false
  }
  return accountCategory.value === 'oauth-based'
})

const isGrokSSOInputMethod = computed(() => form.platform === 'grok' && oauthFlowRef.value?.inputMethod === 'sso_cookie')

const isManualInputMethod = computed(() => {
  return oauthFlowRef.value?.inputMethod === 'manual'
})

const expiresAtInput = computed({
  get: () => formatDateTimeLocal(form.expires_at),
  set: (value: string) => {
    form.expires_at = parseDateTimeLocal(value)
  }
})

const canExchangeCode = computed(() => {
  const authCode = oauthFlowRef.value?.authCode || ''
  if (form.platform === 'openai') {
    return authCode.trim() && openaiOAuth.sessionId.value && !openaiOAuth.loading.value
  }
  if (form.platform === 'gemini') {
    return authCode.trim() && geminiOAuth.sessionId.value && !geminiOAuth.loading.value
  }
  if (form.platform === 'antigravity') {
    return authCode.trim() && antigravityOAuth.sessionId.value && !antigravityOAuth.loading.value
  }
  if (form.platform === 'grok') {
    return authCode.trim() && grokOAuth.sessionId.value && !grokOAuth.loading.value
  }
  return authCode.trim() && oauth.sessionId.value && !oauth.loading.value
})

// Watchers
watch(
  () => props.show,
  (newVal) => {
    if (newVal) {
      // Load TLS fingerprint profiles
      adminAPI.tlsFingerprintProfiles.list()
        .then(profiles => { tlsFingerprintProfiles.value = profiles.map(p => ({ id: p.id, name: p.name })) })
        .catch(() => { tlsFingerprintProfiles.value = [] })
      // Modal opened - fill related models
      allowedModels.value = [...getModelsByPlatform(form.platform)]
      // Antigravity: 默认使用映射模式并填充默认映射
      if (form.platform === 'antigravity') {
        antigravityModelRestrictionMode.value = 'mapping'
        fetchAntigravityDefaultMappings().then(mappings => {
          antigravityModelMappings.value = [...mappings]
        })
        antigravityWhitelistModels.value = []
      } else {
        antigravityWhitelistModels.value = []
        antigravityModelMappings.value = []
        antigravityModelRestrictionMode.value = 'mapping'
      }
    } else {
      resetForm()
    }
  }
)

// Sync form.type based on accountCategory, addMethod, and platform-specific type
watch(
  [accountCategory, addMethod, antigravityAccountType, () => form.platform],
  ([category, method, agType]) => {
    // Antigravity upstream 类型（实际创建为 apikey）
    if (form.platform === 'antigravity' && agType === 'upstream') {
      form.type = 'apikey'
      return
    }
    // Bedrock 类型
    if (form.platform === 'anthropic' && category === 'bedrock') {
      form.type = 'bedrock' as AccountType
      return
    }
    if ((form.platform === 'gemini' || form.platform === 'anthropic') && category === 'service_account') {
      form.type = 'service_account' as AccountType
    } else if (form.platform === 'openai' && category === 'cloudflare') {
      form.type = 'cloudflare'
    } else if (category === 'oauth-based') {
      form.type = form.platform === 'anthropic' ? method as AccountType : 'oauth'
    } else {
      form.type = 'apikey'
    }
  },
  { immediate: true }
)

watch(accountCategory, (category) => {
  if (form.platform !== 'gemini' || category !== 'apikey') {
    geminiUseCustomProtocol.value = false
  }
})

// Reset platform-specific settings when platform changes
watch(
  () => form.platform,
  (newPlatform) => {
    // Reset base URL based on platform
    if (isCNProviderPlatform(newPlatform)) {
      apiKeyBaseUrl.value = defaultCNBaseUrl(newPlatform, accountMode.value, apiProtocol.value)
    } else {
      apiKeyBaseUrl.value =
        (newPlatform === 'openai')
          ? 'https://api.openai.com'
          : newPlatform === 'gemini'
            ? 'https://generativelanguage.googleapis.com'
            : newPlatform === 'grok'
              ? 'https://api.x.ai/v1'
              : newPlatform === 'typesafe'
                ? 'https://api.typesafe.ai'
              : 'https://api.anthropic.com'
      geminiUseCustomProtocol.value = false
    }
    // Clear model-related settings
    allowedModels.value = []
    upstreamModelsPreviewed.value = false
    modelMappings.value = []
    if (newPlatform === 'typesafe') {
      accountCategory.value = 'apikey'
      allowedModels.value = ['jev-latest']
    }
    // Antigravity: 默认使用映射模式并填充默认映射
    if (newPlatform === 'antigravity') {
      antigravityModelRestrictionMode.value = 'mapping'
      fetchAntigravityDefaultMappings().then(mappings => {
        antigravityModelMappings.value = [...mappings]
      })
      antigravityWhitelistModels.value = []
      accountCategory.value = 'oauth-based'
      antigravityAccountType.value = 'oauth'
    } else {
      allowOverages.value = false
      antigravityProjectId.value = ''
      antigravityWhitelistModels.value = []
      antigravityModelMappings.value = []
      antigravityModelRestrictionMode.value = 'mapping'
    }
    if (newPlatform === 'grok') {
      accountCategory.value = 'oauth-based'
      addMethod.value = 'oauth'
      modelRestrictionMode.value = 'mapping'
      form.concurrency = 1
      form.load_factor = null
    }
    if (newPlatform !== 'gemini' && newPlatform !== 'anthropic' && accountCategory.value === 'service_account') {
      accountCategory.value = 'oauth-based'
    }
    if (newPlatform !== 'anthropic' && accountCategory.value === 'bedrock') {
      accountCategory.value = 'oauth-based'
    }
    if (newPlatform !== 'openai' && accountCategory.value === 'cloudflare') {
      accountCategory.value = 'apikey'
    }
    // Reset Bedrock fields when switching platforms
    bedrockAccessKeyId.value = ''
    bedrockSecretAccessKey.value = ''
    bedrockSessionToken.value = ''
    bedrockRegion.value = 'us-east-1'
    bedrockForceGlobal.value = false
    bedrockAuthMode.value = 'sigv4'
    bedrockApiKeyValue.value = ''
    vertexServiceAccountJson.value = ''
    vertexProjectId.value = ''
    vertexClientEmail.value = ''
    vertexLocation.value = 'global'
    // Reset Anthropic/Antigravity-specific settings when switching to other platforms
    if (newPlatform !== 'anthropic' && newPlatform !== 'antigravity') {
      interceptWarmupRequests.value = false
    }
    if (newPlatform !== 'openai') {
      openaiPassthroughEnabled.value = false
      openaiFlattenNamespacesEnabled.value = false
      openAIEndpointCapabilities.value = ['chat_completions', 'embeddings']
      openaiOAuthResponsesWebSocketV2Mode.value = OPENAI_WS_MODE_OFF
      openaiAPIKeyResponsesWebSocketV2Mode.value = OPENAI_WS_MODE_OFF
      codexCLIOnlyEnabled.value = false
      codexCLIOnlyAppServerEnabled.value = false
    }
    if (newPlatform !== 'anthropic') {
      anthropicPassthroughEnabled.value = false
      anthropicAPIKeyAuthScheme.value = 'x_api_key'
      webSearchEmulationMode.value = 'default'
    }
    // 请求头覆写为平台相关配置（常用头集合不同），切换平台时清空，
    // 避免上一平台的配置行被提交到新平台账号
    headerOverrideEnabled.value = false
    headerOverrideRows.value = []
    openAIImagesUrlToB64JsonEnabled.value = false
    grokOAuthCustomBaseUrlEnabled.value = false
    grokOAuthBaseUrl.value = ''
    // Reset OAuth states
    oauth.resetState()
    openaiOAuth.resetState()

    geminiOAuth.resetState()
    antigravityOAuth.resetState()
    grokOAuth.resetState()
  }
)

// Gemini AI Studio OAuth availability (requires operator-configured OAuth client)
watch(
  [accountCategory, () => form.platform],
  ([category, platform]) => {
    if (platform === 'openai' && category !== 'oauth-based') {
      codexCLIOnlyEnabled.value = false
      codexCLIOnlyAppServerEnabled.value = false
    }
    if (platform !== 'anthropic' || category !== 'apikey') {
      anthropicPassthroughEnabled.value = false
      anthropicAPIKeyAuthScheme.value = 'x_api_key'
      webSearchEmulationMode.value = 'default'
    }
  }
)

watch(
  [() => props.show, () => form.platform, accountCategory],
  async ([show, platform, category]) => {
    if (!show || platform !== 'gemini' || category !== 'oauth-based') {
      geminiAIStudioOAuthEnabled.value = false
      return
    }
    const caps = await geminiOAuth.getCapabilities()
    geminiAIStudioOAuthEnabled.value = !!caps?.ai_studio_oauth_enabled
    if (!geminiAIStudioOAuthEnabled.value && geminiOAuthType.value === 'ai_studio') {
      geminiOAuthType.value = 'code_assist'
    }
  },
  { immediate: true }
)

const handleSelectGeminiOAuthType = (oauthType: 'code_assist' | 'google_one' | 'ai_studio') => {
  if (oauthType === 'ai_studio' && !geminiAIStudioOAuthEnabled.value) {
    appStore.showError(t('admin.accounts.oauth.gemini.aiStudioNotConfigured'))
    return
  }
  geminiOAuthType.value = oauthType
}

// Auto-fill related models when switching to whitelist mode or changing platform
watch(
  [modelRestrictionMode, () => form.platform],
  ([newMode]) => {
    if (newMode === 'whitelist') {
      allowedModels.value = [...getModelsByPlatform(form.platform)]
    }
  }
)

watch(
  [antigravityModelRestrictionMode, () => form.platform],
  ([, platform]) => {
    if (platform !== 'antigravity') return
    // Antigravity 默认不做限制：白名单留空表示允许所有（包含未来新增模型）。
    // 如果需要快速填充常用模型，可在组件内点“填充相关模型”。
  }
)

// Model mapping helpers
const addModelMapping = () => {
  modelMappings.value.push({ from: '', to: '' })
}

const addOpenAICompactModelMapping = () => {
  openAICompactModelMappings.value.push({ from: '', to: '' })
}

const removeOpenAICompactModelMapping = (index: number) => {
  openAICompactModelMappings.value.splice(index, 1)
}

const removeModelMapping = (index: number) => {
  modelMappings.value.splice(index, 1)
}

const addPresetMapping = (from: string, to: string) => {
  if (modelMappings.value.some((m) => m.from === from)) {
    appStore.showInfo(t('admin.accounts.mappingExists', { model: from }))
    return
  }
  modelMappings.value.push({ from, to })
}

const importUpstreamModelsToMapping = (models: string[]) => {
  modelMappings.value = mergeUpstreamIdentityMappings(modelMappings.value, models).mappings
}

const importUpstreamModelsToAntigravityMapping = (models: string[]) => {
  antigravityModelMappings.value = mergeUpstreamIdentityMappings(antigravityModelMappings.value, models).mappings
}

const addAntigravityModelMapping = () => {
  antigravityModelMappings.value.push({ from: '', to: '' })
}

const removeAntigravityModelMapping = (index: number) => {
  antigravityModelMappings.value.splice(index, 1)
}

const addAntigravityPresetMapping = (from: string, to: string) => {
  if (antigravityModelMappings.value.some((m) => m.from === from)) {
    appStore.showInfo(t('admin.accounts.mappingExists', { model: from }))
    return
  }
  antigravityModelMappings.value.push({ from, to })
}

// Error code toggle helper
const toggleErrorCode = async (code: number) => {
  const index = selectedErrorCodes.value.indexOf(code)
  if (index === -1) {
    // Adding code - check for 429/529 warning
    if (code === 429) {
      if (!(await confirm(t('admin.accounts.customErrorCodes429Warning')))) {
        return
      }
    } else if (code === 529) {
      if (!(await confirm(t('admin.accounts.customErrorCodes529Warning')))) {
        return
      }
    }
    selectedErrorCodes.value.push(code)
  } else {
    selectedErrorCodes.value.splice(index, 1)
  }
}

// Add custom error code from input
const addCustomErrorCode = async () => {
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
    if (!(await confirm(t('admin.accounts.customErrorCodes429Warning')))) {
      return
    }
  } else if (code === 529) {
    if (!(await confirm(t('admin.accounts.customErrorCodes529Warning')))) {
      return
    }
  }
  selectedErrorCodes.value.push(code)
  customErrorCodeInput.value = null
}

// Remove error code
const removeErrorCode = (code: number) => {
  const index = selectedErrorCodes.value.indexOf(code)
  if (index !== -1) {
    selectedErrorCodes.value.splice(index, 1)
  }
}

const addTempUnschedRule = (preset?: TempUnschedRuleForm) => {
  if (preset) {
    tempUnschedRules.value.push({ ...preset })
    return
  }
  tempUnschedRules.value.push({
    error_code: null,
    keywords: '',
    duration_minutes: 30,
    description: ''
  })
}

const removeTempUnschedRule = (index: number) => {
  tempUnschedRules.value.splice(index, 1)
}

const moveTempUnschedRule = (index: number, direction: number) => {
  const target = index + direction
  if (target < 0 || target >= tempUnschedRules.value.length) return
  const rules = tempUnschedRules.value
  const current = rules[index]
  rules[index] = rules[target]
  rules[target] = current
}

const buildTempUnschedRules = (rules: TempUnschedRuleForm[]) => {
  const out: Array<{
    error_code: number
    keywords: string[]
    duration_minutes: number
    description: string
  }> = []

  for (const rule of rules) {
    const errorCode = Number(rule.error_code)
    const duration = Number(rule.duration_minutes)
    const keywords = splitTempUnschedKeywords(rule.keywords)
    if (!Number.isFinite(errorCode) || errorCode < 100 || errorCode > 599) {
      continue
    }
    if (!Number.isFinite(duration) || duration <= 0) {
      continue
    }
    if (keywords.length === 0) {
      continue
    }
    out.push({
      error_code: Math.trunc(errorCode),
      keywords,
      duration_minutes: Math.trunc(duration),
      description: rule.description.trim()
    })
  }

  return out
}

const applyTempUnschedConfig = (credentials: Record<string, unknown>) => {
  if (!tempUnschedEnabled.value) {
    delete credentials.temp_unschedulable_enabled
    delete credentials.temp_unschedulable_rules
    return true
  }

  const rules = buildTempUnschedRules(tempUnschedRules.value)
  if (rules.length === 0) {
    appStore.showError(t('admin.accounts.tempUnschedulable.rulesInvalid'))
    return false
  }

  credentials.temp_unschedulable_enabled = true
  credentials.temp_unschedulable_rules = rules
  return true
}

const splitTempUnschedKeywords = (value: string) => {
  return value
    .split(/[,;]/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0)
}

const needsMixedChannelCheck = (platform: AccountPlatform) => platform === 'antigravity' || platform === 'anthropic'

const buildMixedChannelDetails = (resp?: CheckMixedChannelResponse) => {
  const details = resp?.details
  if (!details) {
    return null
  }
  return {
    groupName: details.group_name || 'Unknown',
    currentPlatform: details.current_platform || 'Unknown',
    otherPlatform: details.other_platform || 'Unknown'
  }
}

const clearMixedChannelDialog = () => {
  showMixedChannelWarning.value = false
  mixedChannelWarningDetails.value = null
  mixedChannelWarningRawMessage.value = ''
  mixedChannelWarningAction.value = null
}

const openMixedChannelDialog = (opts: {
  response?: CheckMixedChannelResponse
  message?: string
  onConfirm: () => Promise<void>
}) => {
  mixedChannelWarningDetails.value = buildMixedChannelDetails(opts.response)
  mixedChannelWarningRawMessage.value =
    opts.message || opts.response?.message || t('admin.accounts.failedToCreate')
  mixedChannelWarningAction.value = opts.onConfirm
  showMixedChannelWarning.value = true
}

const withAntigravityConfirmFlag = (payload: CreateAccountRequest): CreateAccountRequest => {
  if (needsMixedChannelCheck(payload.platform) && antigravityMixedChannelConfirmed.value) {
    return {
      ...payload,
      confirm_mixed_channel_risk: true
    }
  }
  const cloned = { ...payload }
  delete cloned.confirm_mixed_channel_risk
  return cloned
}

const ensureAntigravityMixedChannelConfirmed = async (onConfirm: () => Promise<void>): Promise<boolean> => {
  if (!needsMixedChannelCheck(form.platform)) {
    return true
  }
  if (antigravityMixedChannelConfirmed.value) {
    return true
  }

  try {
    const result = await adminAPI.accounts.checkMixedChannelRisk({
      platform: form.platform,
      group_ids: form.group_ids
    })
    if (!result.has_risk) {
      return true
    }
    openMixedChannelDialog({
      response: result,
      onConfirm: async () => {
        antigravityMixedChannelConfirmed.value = true
        await onConfirm()
      }
    })
    return false
  } catch (error: any) {
    appStore.showError(error.response?.data?.message || error.response?.data?.detail || t('admin.accounts.failedToCreate'))
    return false
  }
}

const submitCreateAccount = async (payload: CreateAccountRequest) => {
  submitting.value = true
  try {
    const account = await adminAPI.accounts.create(withAntigravityConfirmFlag(payload))
    const modelMapping = payload.credentials.model_mapping
    const hasConcreteMappedTarget = payload.type === 'apikey' &&
      typeof modelMapping === 'object' &&
      modelMapping !== null &&
      Object.values(modelMapping).some((target) =>
        typeof target === 'string' && target.trim() !== '' && !target.includes('*')
      )
    if (upstreamModelsPreviewed.value || hasConcreteMappedTarget) {
      try {
        const result = await adminAPI.accounts.syncUpstreamModels(account.id)
        const warnings = result.warnings ?? []
        if (warnings.some(warning => warning.code === 'upstream_model_metadata_incomplete')) {
          appStore.showWarning(t('admin.accounts.syncUpstreamModelsMetadataIncomplete'))
        } else if (warnings.some(warning => warning.code === 'upstream_model_metadata_partial')) {
          appStore.showWarning(t('admin.accounts.syncUpstreamModelsMetadataPartial'))
        }
      } catch {
        appStore.showWarning(t('admin.accounts.syncUpstreamModelsFailed'))
      }
    }
    if (
      payload.type === 'apikey' &&
      payload.upstream_billing_probe_enabled === true
    ) {
      try {
        await adminAPI.accounts.probeUpstreamBilling(account.id)
      } catch {
        appStore.showWarning(t('admin.accounts.upstreamBilling.probeFailed'))
      }
    }
    appStore.showSuccess(t('admin.accounts.accountCreated'))
    emit('created')
    handleClose()
  } catch (error: any) {
    if (error.response?.status === 409 && error.response?.data?.error === 'mixed_channel_warning' && needsMixedChannelCheck(form.platform)) {
      openMixedChannelDialog({
        message: error.response?.data?.message,
        onConfirm: async () => {
          antigravityMixedChannelConfirmed.value = true
          await submitCreateAccount(payload)
        }
      })
      return
    }
    appStore.showError(error.response?.data?.message || error.response?.data?.detail || t('admin.accounts.failedToCreate'))
  } finally {
    submitting.value = false
  }
}

// Methods
const resetForm = () => {
  step.value = 1
  form.name = ''
  form.notes = ''
  form.platform = 'anthropic'
  form.type = 'oauth'
  copilotSDKEnabled.value = false
  form.credentials = {}
  geminiCacheSettings.value = readGeminiCacheSettings()
  form.proxy_id = null
  form.concurrency = 10
  form.load_factor = null
  form.priority = 1
  form.rate_multiplier = 1
  form.group_ids = []
  form.expires_at = null
  accountCategory.value = 'oauth-based'
  addMethod.value = 'oauth'
  geminiUseCustomProtocol.value = false
  accountMode.value = 'payg'
  apiProtocol.value = 'adaptive'
  adaptiveBaseUrls.value = { chat_completions: '', anthropic: '', responses: '' }
  apiKeyBaseUrl.value = 'https://api.anthropic.com'
  apiKeyValue.value = ''
  cloudflareAccountID.value = ''
  upstreamRequestIdHeader.value = ''
  upstreamBillingAutoProbeEnabled.value = true
  editQuotaLimit.value = null
  editQuotaDailyLimit.value = null
  editQuotaWeeklyLimit.value = null
  editDailyResetMode.value = null
  editDailyResetHour.value = null
  editWeeklyResetMode.value = null
  editWeeklyResetDay.value = null
  editWeeklyResetHour.value = null
  editResetTimezone.value = null
  modelMappings.value = []
  openAICompactModelMappings.value = []
  modelRestrictionMode.value = 'whitelist'
  allowedModels.value = [...claudeModels] // Default fill related models

  antigravityModelRestrictionMode.value = 'mapping'
  antigravityWhitelistModels.value = []
  fetchAntigravityDefaultMappings().then(mappings => {
    antigravityModelMappings.value = [...mappings]
  })
  poolModeEnabled.value = false
  poolModeRetryCount.value = DEFAULT_POOL_MODE_RETRY_COUNT
  poolModeRetryStatusCodesInput.value = ''
  customErrorCodesEnabled.value = false
  selectedErrorCodes.value = []
  customErrorCodeInput.value = null
  headerOverrideEnabled.value = false
  headerOverrideRows.value = []
  openAIImagesUrlToB64JsonEnabled.value = false
  grokOAuthCustomBaseUrlEnabled.value = false
  grokOAuthBaseUrl.value = ''
  interceptWarmupRequests.value = false
  autoPauseOnExpired.value = true
  openaiPassthroughEnabled.value = false
  openaiFlattenNamespacesEnabled.value = false
  openAILongContextBillingEnabled.value = false
  openAILongContextBillingTouched.value = false
  openAICompactMode.value = 'auto'
  openAIResponsesMode.value = 'auto'
  openAIEndpointCapabilities.value = ['chat_completions', 'embeddings']
  openaiOAuthResponsesWebSocketV2Mode.value = OPENAI_WS_MODE_OFF
  openaiAPIKeyResponsesWebSocketV2Mode.value = OPENAI_WS_MODE_OFF
  codexCLIOnlyEnabled.value = false
  codexCLIOnlyAppServerEnabled.value = false
  codexFingerprintMode.value = 'off'
  anthropicPassthroughEnabled.value = false
  anthropicAPIKeyAuthScheme.value = 'x_api_key'
  webSearchEmulationMode.value = 'default'
  // Reset quota control state
  windowCostEnabled.value = false
  windowCostLimit.value = null
  windowCostStickyReserve.value = null
  sessionLimitEnabled.value = false
  maxSessions.value = null
  sessionIdleTimeout.value = null
  rpmLimitEnabled.value = false
  baseRpm.value = null
  rpmStrategy.value = 'tiered'
  rpmStickyBuffer.value = null
  userMsgQueueMode.value = ''
  tlsFingerprintEnabled.value = false
  tlsFingerprintProfileId.value = null
  sessionIdMaskingEnabled.value = false
  cacheTTLOverrideEnabled.value = false
  cacheTTLOverrideTarget.value = '5m'
  customBaseUrlEnabled.value = false
  customBaseUrl.value = ''
  allowOverages.value = false
  antigravityAccountType.value = 'oauth'
  antigravityProjectId.value = ''
  upstreamBaseUrl.value = ''
  upstreamApiKey.value = ''
  vertexServiceAccountJson.value = ''
  vertexProjectId.value = ''
  vertexClientEmail.value = ''
  vertexLocation.value = 'global'
  tempUnschedEnabled.value = false
  tempUnschedRules.value = []
  geminiOAuthType.value = 'code_assist'
  geminiTierGoogleOne.value = 'google_one_free'
  geminiTierGcp.value = 'gcp_standard'
  geminiTierAIStudio.value = 'aistudio_free'
  oauth.resetState()
  openaiOAuth.resetState()
  geminiOAuth.resetState()
  antigravityOAuth.resetState()
  grokOAuth.resetState()
  oauthFlowRef.value?.reset()
  antigravityMixedChannelConfirmed.value = false
  upstreamModelsPreviewed.value = false
  clearMixedChannelDialog()
}

const handleClose = () => {
  antigravityMixedChannelConfirmed.value = false
  clearMixedChannelDialog()
  emit('close')
}

const buildOpenAIExtra = (base?: Record<string, unknown>): Record<string, unknown> | undefined => {
  if (form.platform !== 'openai') {
    return base
  }

  const extra: Record<string, unknown> = { ...(base || {}) }
  if (accountCategory.value === 'apikey' && copilotSDKEnabled.value) extra.openai_copilot_sdk = true
  else delete extra.openai_copilot_sdk
  if (accountCategory.value === 'oauth-based') {
    extra.openai_oauth_responses_websockets_v2_mode = openaiOAuthResponsesWebSocketV2Mode.value
    extra.openai_oauth_responses_websockets_v2_enabled = isOpenAIWSModeEnabled(openaiOAuthResponsesWebSocketV2Mode.value)
  } else if (accountCategory.value === 'apikey') {
    extra.openai_apikey_responses_websockets_v2_mode = openaiAPIKeyResponsesWebSocketV2Mode.value
    extra.openai_apikey_responses_websockets_v2_enabled = isOpenAIWSModeEnabled(openaiAPIKeyResponsesWebSocketV2Mode.value)
  }
  // 清理兼容旧键，统一改用分类型开关。
  delete extra.responses_websockets_v2_enabled
  delete extra.openai_ws_enabled
  if (openaiPassthroughEnabled.value) {
    extra.openai_passthrough = true
  } else {
    delete extra.openai_passthrough
    delete extra.openai_oauth_passthrough
  }
  // 缺省即保留 namespace，不写空值，避免 extra 里堆积默认项
  if (form.type === 'oauth' && openaiFlattenNamespacesEnabled.value) {
    extra.openai_responses_flatten_namespaces = true
  } else {
    delete extra.openai_responses_flatten_namespaces
  }
  extra.openai_long_context_billing_enabled = openAILongContextBillingEnabled.value

  if (accountCategory.value === 'oauth-based' && codexCLIOnlyEnabled.value) {
    extra.codex_cli_only = true
  } else {
    delete extra.codex_cli_only
  }
  delete extra.codex_cli_only_allowed_clients
  if (
    accountCategory.value === 'oauth-based' &&
    codexCLIOnlyEnabled.value &&
    codexCLIOnlyAppServerEnabled.value
  ) {
    extra.codex_cli_only_allow_app_server = true
  } else {
    delete extra.codex_cli_only_allow_app_server
  }
  // 收敛是显式 opt-in：off 即默认值，不落键；device/session/full 必须显式写入，
  // 否则管理员的选择会被当成默认而丢失（#5610）。
  if (codexFingerprintMode.value !== 'off') {
    extra.codex_fingerprint_mode = codexFingerprintMode.value
  } else {
    delete extra.codex_fingerprint_mode
  }
  if (openAICompactMode.value !== 'auto') {
    extra.openai_compact_mode = openAICompactMode.value
  } else {
    delete extra.openai_compact_mode
  }

  if (
    accountCategory.value === 'apikey' &&
    openAITextGenerationCapabilityEnabled.value &&
    openAIResponsesMode.value !== 'auto'
  ) {
    extra.openai_responses_mode = openAIResponsesMode.value
  } else {
    delete extra.openai_responses_mode
  }
  if (accountCategory.value === 'apikey' && openAIImagesUrlToB64JsonEnabled.value) {
    extra.images_url_to_b64_json = true
  } else {
    delete extra.images_url_to_b64_json
  }

  return Object.keys(extra).length > 0 ? extra : undefined
}

const buildOpenAICodexImportExtra = (): Record<string, unknown> | undefined => {
  const extra = buildOpenAIExtra()
  if (!extra) {
    return undefined
  }
  if (!openAILongContextBillingTouched.value) {
    delete extra.openai_long_context_billing_enabled
  }
  return Object.keys(extra).length > 0 ? extra : undefined
}

const buildAnthropicExtra = (base?: Record<string, unknown>): Record<string, unknown> | undefined => {
  if (form.platform !== 'anthropic' || accountCategory.value !== 'apikey') {
    return base
  }

  const extra: Record<string, unknown> = { ...(base || {}) }
  if (anthropicPassthroughEnabled.value) {
    extra.anthropic_passthrough = true
  } else {
    delete extra.anthropic_passthrough
  }
  if (anthropicAPIKeyAuthScheme.value === 'authorization_bearer') {
    extra.anthropic_apikey_auth_scheme = 'authorization_bearer'
  } else {
    delete extra.anthropic_apikey_auth_scheme
  }
  if (webSearchEmulationMode.value === 'default') {
    delete extra.web_search_emulation
  } else {
    extra.web_search_emulation = webSearchEmulationMode.value
  }

  return Object.keys(extra).length > 0 ? extra : undefined
}

// Helper function to create account with mixed channel warning handling
const doCreateAccount = async (payload: CreateAccountRequest) => {
  const canContinue = await ensureAntigravityMixedChannelConfirmed(async () => {
    await submitCreateAccount(payload)
  })
  if (!canContinue) {
    return
  }
  await submitCreateAccount(payload)
}

// Handle mixed channel warning confirmation
const handleMixedChannelConfirm = async () => {
  const action = mixedChannelWarningAction.value
  if (!action) {
    clearMixedChannelDialog()
    return
  }
  clearMixedChannelDialog()
  submitting.value = true
  try {
    await action()
  } finally {
    submitting.value = false
  }
}

const handleMixedChannelCancel = () => {
  clearMixedChannelDialog()
}

const normalizePoolModeRetryCount = (value: number) => {
  if (!Number.isFinite(value)) {
    return DEFAULT_POOL_MODE_RETRY_COUNT
  }
  const normalized = Math.trunc(value)
  if (normalized < 0) {
    return 0
  }
  if (normalized > MAX_POOL_MODE_RETRY_COUNT) {
    return MAX_POOL_MODE_RETRY_COUNT
  }
  return normalized
}

const applyVertexServiceAccountJson = (value: string) => {
  const raw = value.trim()
  if (!raw) {
    vertexProjectId.value = ''
    vertexClientEmail.value = ''
    return false
  }
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const projectId = typeof parsed.project_id === 'string' ? parsed.project_id.trim() : ''
    const clientEmail = typeof parsed.client_email === 'string' ? parsed.client_email.trim() : ''
    const privateKey = typeof parsed.private_key === 'string' ? parsed.private_key.trim() : ''
    if (!projectId || !clientEmail || !privateKey) {
      appStore.showError(t('admin.accounts.vertexSaJsonMissingFields'))
      return false
    }
    vertexProjectId.value = projectId
    vertexClientEmail.value = clientEmail
    vertexServiceAccountJson.value = JSON.stringify(parsed)
    return true
  } catch {
    appStore.showError(t('admin.accounts.vertexSaJsonInvalid'))
    return false
  }
}

const parseVertexServiceAccountJson = () => applyVertexServiceAccountJson(vertexServiceAccountJson.value)

const applyAPIKeyQuotaExtra = (extra: Record<string, unknown> | undefined, type: AccountType) => {
  if (type !== 'apikey' && type !== 'cloudflare' && type !== 'bedrock') {
    return extra
  }
  const quotaExtra: Record<string, unknown> = { ...(extra || {}) }
  if (editQuotaLimit.value != null && editQuotaLimit.value > 0) {
    quotaExtra.quota_limit = editQuotaLimit.value
  }
  if (editQuotaDailyLimit.value != null && editQuotaDailyLimit.value > 0) {
    quotaExtra.quota_daily_limit = editQuotaDailyLimit.value
  }
  if (editQuotaWeeklyLimit.value != null && editQuotaWeeklyLimit.value > 0) {
    quotaExtra.quota_weekly_limit = editQuotaWeeklyLimit.value
  }
  if (editDailyResetMode.value === 'fixed') {
    quotaExtra.quota_daily_reset_mode = 'fixed'
    quotaExtra.quota_daily_reset_hour = editDailyResetHour.value ?? 0
  }
  if (editWeeklyResetMode.value === 'fixed') {
    quotaExtra.quota_weekly_reset_mode = 'fixed'
    quotaExtra.quota_weekly_reset_day = editWeeklyResetDay.value ?? 1
    quotaExtra.quota_weekly_reset_hour = editWeeklyResetHour.value ?? 0
  }
  if (editDailyResetMode.value === 'fixed' || editWeeklyResetMode.value === 'fixed') {
    quotaExtra.quota_reset_timezone = editResetTimezone.value || 'UTC'
  }
  writeQuotaNotifyToExtra(quotaExtra, 'create')
  return Object.keys(quotaExtra).length > 0 ? quotaExtra : extra
}

const handleVertexServiceAccountFile = async (event: Event) => {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  try {
    applyVertexServiceAccountJson(await file.text())
  } finally {
    input.value = ''
  }
}

const handleVertexServiceAccountDrop = async (event: DragEvent) => {
  vertexServiceAccountDragActive.value = false
  const file = event.dataTransfer?.files?.[0]
  if (!file) return
  applyVertexServiceAccountJson(await file.text())
}

const handleSubmit = async () => {
  // For OAuth-based type, handle OAuth flow (goes to step 2)
  if (isOAuthFlow.value) {
    if (!isGrokSSOInputMethod.value && !form.name.trim()) {
      appStore.showError(t('admin.accounts.pleaseEnterAccountName'))
      return
    }
    const canContinue = await ensureAntigravityMixedChannelConfirmed(async () => {
      step.value = 2
    })
    if (!canContinue) {
      return
    }
    step.value = 2
    return
  }

  // For Bedrock type, create directly
  if (form.platform === 'anthropic' && accountCategory.value === 'bedrock') {
    if (!form.name.trim()) {
      appStore.showError(t('admin.accounts.pleaseEnterAccountName'))
      return
    }

    const credentials: Record<string, unknown> = {
      auth_mode: bedrockAuthMode.value,
      aws_region: bedrockRegion.value.trim() || 'us-east-1',
    }

    if (bedrockAuthMode.value === 'sigv4') {
      if (!bedrockAccessKeyId.value.trim()) {
        appStore.showError(t('admin.accounts.bedrockAccessKeyIdRequired'))
        return
      }
      if (!bedrockSecretAccessKey.value.trim()) {
        appStore.showError(t('admin.accounts.bedrockSecretAccessKeyRequired'))
        return
      }
      credentials.aws_access_key_id = bedrockAccessKeyId.value.trim()
      credentials.aws_secret_access_key = bedrockSecretAccessKey.value.trim()
      if (bedrockSessionToken.value.trim()) {
        credentials.aws_session_token = bedrockSessionToken.value.trim()
      }
    } else {
      if (!bedrockApiKeyValue.value.trim()) {
        appStore.showError(t('admin.accounts.bedrockApiKeyRequired'))
        return
      }
      credentials.api_key = bedrockApiKeyValue.value.trim()
    }

    if (bedrockForceGlobal.value) {
      credentials.aws_force_global = 'true'
    }

    // Model mapping
    const modelMapping = buildModelMappingObject(
      modelRestrictionMode.value, allowedModels.value, modelMappings.value
    )
    if (modelMapping) {
      credentials.model_mapping = modelMapping
    }

    // Pool mode
    if (poolModeEnabled.value) {
      credentials.pool_mode = true
      credentials.pool_mode_retry_count = normalizePoolModeRetryCount(poolModeRetryCount.value)
      const parsedRetryStatusCodes = parsePoolModeRetryStatusCodes(poolModeRetryStatusCodesInput.value)
      if (parsedRetryStatusCodes.length > 0) {
        credentials.pool_mode_retry_status_codes = parsedRetryStatusCodes
      }
    }

    applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')

    await createAccountAndFinish('anthropic', 'bedrock' as AccountType, credentials)
    return
  }

  // For Antigravity upstream type, create directly
  if (form.platform === 'antigravity' && antigravityAccountType.value === 'upstream') {
    if (!form.name.trim()) {
      appStore.showError(t('admin.accounts.pleaseEnterAccountName'))
      return
    }
    if (!upstreamBaseUrl.value.trim()) {
      appStore.showError(t('admin.accounts.upstream.pleaseEnterBaseUrl'))
      return
    }
    if (!upstreamApiKey.value.trim()) {
      appStore.showError(t('admin.accounts.upstream.pleaseEnterApiKey'))
      return
    }

    // Build upstream credentials (and optional model restriction)
    const credentials: Record<string, unknown> = {
      base_url: upstreamBaseUrl.value.trim(),
      api_key: upstreamApiKey.value.trim()
    }

    // Antigravity 只使用映射模式
    const antigravityModelMapping = buildModelMappingObject(
      'mapping',
      [],
      antigravityModelMappings.value
    )
    if (antigravityModelMapping) {
      credentials.model_mapping = antigravityModelMapping
    }

    applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')

    const extra = buildAntigravityExtra()
    await createAccountAndFinish(form.platform, 'apikey', credentials, extra)
    return
  }

  if ((form.platform === 'gemini' || form.platform === 'anthropic') && accountCategory.value === 'service_account') {
    if (!form.name.trim()) {
      appStore.showError(t('admin.accounts.pleaseEnterAccountName'))
      return
    }
    if (!parseVertexServiceAccountJson()) {
      return
    }
    if (!vertexLocation.value.trim()) {
      appStore.showError(t('admin.accounts.vertexLocationRequired'))
      return
    }
    const credentials: Record<string, unknown> = {
      service_account_json: vertexServiceAccountJson.value.trim(),
      project_id: vertexProjectId.value.trim(),
      client_email: vertexClientEmail.value.trim(),
      location: vertexLocation.value.trim(),
      tier_id: 'vertex'
    }
    await createAccountAndFinish(form.platform, 'service_account' as AccountType, credentials)
    return
  }

  // For apikey type, create directly
  if (!apiKeyValue.value.trim()) {
    appStore.showError(t('admin.accounts.pleaseEnterApiKey'))
    return
  }

  // Determine default base URL based on platform
  const defaultBaseUrl = form.platform === 'openai'
    ? 'https://api.openai.com'
    : form.platform === 'gemini'
      ? 'https://generativelanguage.googleapis.com'
      : form.platform === 'grok'
        ? 'https://api.x.ai/v1'
        : 'https://api.anthropic.com'

  // Build credentials with optional model mapping
  const credentials: Record<string, unknown> = {
    api_key: apiKeyValue.value.trim()
  }
  if (form.type === 'cloudflare') {
    const accountID = cloudflareAccountID.value.trim()
    if (!/^[A-Za-z0-9_-]{8,64}$/.test(accountID)) {
      appStore.showError(t('admin.accounts.openai.cloudflareAccountIdInvalid'))
      return
    }
    credentials.account_id = accountID
  } else {
    credentials.base_url = apiKeyBaseUrl.value.trim() || defaultBaseUrl
  }
  if (form.platform === 'gemini' && !isGeminiProtocolAccount.value) {
    credentials.tier_id = geminiTierAIStudio.value
  }

  // CN providers / Gemini custom upstream: write account mode + protocol + endpoints.
  if (isAdaptiveProtocolAccount.value && isAdaptiveProtocolPlatform(form.platform)) {
    if (isCNPlatform.value) {
      credentials.account_mode = accountMode.value
    }
    credentials.api_protocol = apiProtocol.value
    if (apiProtocol.value === 'adaptive') {
      const defaults = defaultCNAdaptiveBaseUrls(form.platform, accountMode.value)
      const protocolBaseUrls: Record<string, string> = {}
      for (const item of adaptiveProtocolEndpointOptions.value) {
        protocolBaseUrls[item.value] = (adaptiveBaseUrls.value[item.value] || defaults[item.value]).trim()
      }
      credentials.api_base_urls = protocolBaseUrls
      credentials.base_url = protocolBaseUrls.chat_completions || protocolBaseUrls.responses
    }
    const resolvedCNBase = (
      apiKeyBaseUrl.value.trim() || defaultCNBaseUrl(form.platform, accountMode.value, apiProtocol.value)
    ).trim()
    if (apiProtocol.value !== 'adaptive' && resolvedCNBase) {
      credentials.base_url = resolvedCNBase
    }
    // 智谱团队版 Coding Plan：组织/项目 ID 写入凭据（非空才写）
    if (form.platform === 'zhipu' && accountMode.value === 'coding') {
      if (zhipuOrganization.value.trim()) credentials.zhipu_organization = zhipuOrganization.value.trim()
      if (zhipuProject.value.trim()) credentials.zhipu_project = zhipuProject.value.trim()
    }
  }

  // Add model mapping if configured（OpenAI 开启自动透传时不应用）
  if (!isOpenAIModelRestrictionDisabled.value) {
    const modelMapping = buildModelMappingObject(modelRestrictionMode.value, allowedModels.value, modelMappings.value)
    if (modelMapping) {
      credentials.model_mapping = modelMapping
    }
  }
  if (form.platform === 'openai' && form.type !== 'cloudflare') {
    applyOpenAIEndpointCapabilities(credentials)
    const compactModelMapping = buildOpenAICompactModelMapping()
    if (compactModelMapping) {
      credentials.compact_model_mapping = compactModelMapping
    }
  }

  // Add pool mode if enabled
  if (poolModeEnabled.value) {
    credentials.pool_mode = true
    credentials.pool_mode_retry_count = normalizePoolModeRetryCount(poolModeRetryCount.value)
    const parsedRetryStatusCodes = parsePoolModeRetryStatusCodes(poolModeRetryStatusCodesInput.value)
    if (parsedRetryStatusCodes.length > 0) {
      credentials.pool_mode_retry_status_codes = parsedRetryStatusCodes
    }
  }

  // Add custom error codes if enabled
  if (customErrorCodesEnabled.value) {
    credentials.custom_error_codes_enabled = true
    credentials.custom_error_codes = [...selectedErrorCodes.value]
  }

  // Add header override if enabled for this API-key platform
  if (isHeaderOverrideCapable(form.platform, 'apikey')) {
    if (headerOverrideEnabled.value) {
      const headerError = validateHeaderOverrideRows(headerOverrideRows.value)
      if (headerError) {
        appStore.showError(t(`admin.accounts.headerOverride.${headerError}`))
        return
      }
    }
    applyHeaderOverride(credentials, headerOverrideEnabled.value, headerOverrideRows.value, 'create')
  }

  applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')
  if (!applyTempUnschedConfig(credentials)) {
    return
  }

  applyGeminiCacheSettings(credentials, geminiCacheSettings.value, form.platform, !geminiUseCustomProtocol.value)
  form.credentials = credentials
  const extra = buildAnthropicExtra(buildOpenAIExtra())

  await doCreateAccount({
    ...form,
    platform: form.platform,
    type: form.type,
    group_ids: form.group_ids,
    extra: applyAPIKeyQuotaExtra(withUpstreamRequestIdHeader(extra), form.type),
    upstream_billing_probe_enabled: form.type === 'cloudflare' ? false : upstreamBillingAutoProbeEnabled.value,
    auto_pause_on_expired: autoPauseOnExpired.value
  })
}

const goBackToBasicInfo = () => {
  step.value = 1
  oauth.resetState()
  openaiOAuth.resetState()
  geminiOAuth.resetState()
  antigravityOAuth.resetState()
  grokOAuth.resetState()
  oauthFlowRef.value?.reset()
}

const handleGenerateUrl = async () => {
  if (form.platform === 'openai') {
    await openaiOAuth.generateAuthUrl(form.proxy_id)
  } else if (form.platform === 'gemini') {
    await geminiOAuth.generateAuthUrl(
      form.proxy_id,
      oauthFlowRef.value?.projectId,
      geminiOAuthType.value,
      geminiSelectedTier.value
    )
  } else if (form.platform === 'antigravity') {
    await antigravityOAuth.generateAuthUrl(form.proxy_id)
  } else if (form.platform === 'grok') {
    await grokOAuth.generateAuthUrl(form.proxy_id)
  } else {
    await oauth.generateAuthUrl(addMethod.value, form.proxy_id)
  }
}

const handleValidateRefreshToken = (rt: string) => {
  if (form.platform === 'openai') {
    handleOpenAIValidateRT(rt)
  } else if (form.platform === 'antigravity') {
    handleAntigravityValidateRT(rt)
  } else if (form.platform === 'grok') {
    handleGrokValidateRT(rt)
  }
}

const handleValidateSessionToken = (_sessionToken: string) => {
  // Session token validation removed
}

const formatDateTimeLocal = formatDateTimeLocalInput
const parseDateTimeLocal = parseDateTimeLocalInput

// Create account and handle success/failure
const createAccountAndFinish = async (
  platform: AccountPlatform,
  type: AccountType,
  credentials: Record<string, unknown>,
  extra?: Record<string, unknown>
) => {
  if (!applyTempUnschedConfig(credentials)) {
    return
  }
  // Inject quota limits for apikey/bedrock accounts
  let finalExtra = withUpstreamRequestIdHeader(extra)
  finalExtra = applyAPIKeyQuotaExtra(finalExtra, type)
  if (platform === 'openai') {
    if (type === 'apikey') {
      applyOpenAIEndpointCapabilities(credentials)
    }
    const compactModelMapping = buildOpenAICompactModelMapping()
    if (compactModelMapping) {
      credentials.compact_model_mapping = compactModelMapping
    } else {
      delete credentials.compact_model_mapping
    }
  }
  if (platform === 'grok') {
    if (!credentials.base_url) {
      credentials.base_url = apiKeyBaseUrl.value.trim() || 'https://api.x.ai/v1'
    }
    const modelMapping = buildModelMappingObject(modelRestrictionMode.value, allowedModels.value, modelMappings.value)
    if (modelMapping) {
      credentials.model_mapping = modelMapping
    } else {
      delete credentials.model_mapping
    }
  }
  await doCreateAccount({
    name: form.name,
    notes: form.notes,
    platform,
    type,
    credentials,
    extra: finalExtra,
    proxy_id: form.proxy_id,
    concurrency: form.concurrency,
    load_factor: form.load_factor ?? undefined,
    priority: form.priority,
    rate_multiplier: form.rate_multiplier,
    group_ids: form.group_ids,
    expires_at: form.expires_at,
    // 上游倍率探测对全部 API-key 平台开放（antigravity upstream 走本 helper）；
    // 非 apikey 类型（bedrock/oauth）不传，后端不动作。
    upstream_billing_probe_enabled: type === 'apikey' ? upstreamBillingAutoProbeEnabled.value : undefined,
    auto_pause_on_expired: autoPauseOnExpired.value
  })
}

// Grok 手动 RT 批量验证和创建
const handleGrokValidateRT = async (refreshTokenInput: string) => {
  if (!refreshTokenInput.trim()) return

  const refreshTokens = refreshTokenInput
    .split('\n')
    .map((rt) => rt.trim())
    .filter((rt) => rt)

  if (refreshTokens.length === 0) {
    grokOAuth.error.value = t('admin.accounts.oauth.grok.pleaseEnterRefreshToken')
    return
  }
  if (!validateGrokOAuthUpstreamConfig()) return

  grokOAuth.loading.value = true
  grokOAuth.error.value = ''

  let successCount = 0
  let failedCount = 0
  const errors: string[] = []

  try {
    for (let i = 0; i < refreshTokens.length; i++) {
      try {
        const tokenInfo = await grokOAuth.validateRefreshToken(refreshTokens[i], form.proxy_id)
        if (!tokenInfo) {
          failedCount++
          errors.push(`#${i + 1}: ${grokOAuth.error.value || 'Validation failed'}`)
          grokOAuth.error.value = ''
          continue
        }

        const credentials = grokOAuth.buildCredentials(tokenInfo)
        applyGrokOAuthUpstreamConfig(credentials)
        const extra = grokOAuth.buildExtraInfo(tokenInfo)
        const accountName = refreshTokens.length > 1 ? `${form.name || tokenInfo.email || 'Grok OAuth Account'} #${i + 1}` : (form.name || tokenInfo.email || 'Grok OAuth Account')

        const modelMapping = buildModelMappingObject(modelRestrictionMode.value, allowedModels.value, modelMappings.value)
        if (modelMapping) {
          credentials.model_mapping = modelMapping
        }
        if (!applyTempUnschedConfig(credentials)) {
          return
        }

        await adminAPI.accounts.create({
          name: accountName,
          notes: form.notes,
          platform: 'grok',
          type: 'oauth',
          credentials,
          extra: withUpstreamRequestIdHeader(extra),
          proxy_id: form.proxy_id,
          concurrency: form.concurrency,
          load_factor: form.load_factor ?? undefined,
          priority: form.priority,
          rate_multiplier: form.rate_multiplier,
          group_ids: form.group_ids,
          expires_at: form.expires_at,
          auto_pause_on_expired: autoPauseOnExpired.value
        })
        successCount++
      } catch (error: any) {
        failedCount++
        const errMsg = error.response?.data?.detail || error.message || 'Unknown error'
        errors.push(`#${i + 1}: ${errMsg}`)
      }
    }

    if (successCount > 0 && failedCount === 0) {
      appStore.showSuccess(
        refreshTokens.length > 1
          ? t('admin.accounts.oauth.batchSuccess', { count: successCount })
          : t('admin.accounts.accountCreated')
      )
      emit('created')
      handleClose()
    } else if (successCount > 0) {
      appStore.showWarning(t('admin.accounts.oauth.batchPartialSuccess', { success: successCount, failed: failedCount }))
      grokOAuth.error.value = errors.join('\n')
      emit('created')
    } else {
      grokOAuth.error.value = errors.join('\n')
      appStore.showError(t('admin.accounts.oauth.batchFailed'))
    }
  } finally {
    grokOAuth.loading.value = false
  }
}

const handleGrokImportSSO = async (ssoInput: string) => {
  // Align with OpenAI/Grok RT batch import: one token per line, no client-side dedupe.
  const ssoTokens = ssoInput
    .split('\n')
    .map((token) => token.trim())
    .filter((token) => token)
  if (ssoTokens.length === 0) return
  if (!validateGrokOAuthUpstreamConfig()) return

  grokOAuth.loading.value = true
  grokOAuth.error.value = ''

  const credentials: Record<string, unknown> = {}
  applyGrokOAuthUpstreamConfig(credentials)
  const modelMapping = buildModelMappingObject(modelRestrictionMode.value, allowedModels.value, modelMappings.value)
  if (modelMapping) {
    credentials.model_mapping = modelMapping
  }
  if (!applyTempUnschedConfig(credentials)) {
    grokOAuth.loading.value = false
    return
  }

  try {
    const result = await adminAPI.grok.createFromSSO({
      sso_tokens: ssoTokens,
      name: form.name || undefined,
      notes: form.notes || undefined,
      proxy_id: form.proxy_id,
      group_ids: form.group_ids,
      credentials,
      concurrency: form.concurrency,
      load_factor: form.load_factor ?? undefined,
      priority: form.priority,
      rate_multiplier: form.rate_multiplier,
      expires_at: form.expires_at,
      auto_pause_on_expired: autoPauseOnExpired.value
    })

    const successCount = result.created?.length || 0
    const failedCount = result.failed?.length || 0
    if (successCount > 0 && failedCount === 0) {
      appStore.showSuccess(
        ssoTokens.length > 1
          ? t('admin.accounts.oauth.batchSuccess', { count: successCount })
          : t('admin.accounts.accountCreated')
      )
      emit('created')
      handleClose()
    } else if (successCount > 0 && failedCount > 0) {
      // Same as OpenAI/Grok RT: keep input, show failures, refresh list.
      appStore.showWarning(
        t('admin.accounts.oauth.batchPartialSuccess', { success: successCount, failed: failedCount })
      )
      grokOAuth.error.value = (result.failed || [])
        .map((item) => `#${item.index}: ${item.error || 'Unknown error'}`)
        .join('\n')
      emit('created')
    } else {
      grokOAuth.error.value = (result.failed || [])
        .map((item) => `#${item.index}: ${item.error || 'Unknown error'}`)
        .join('\n') || t('admin.accounts.oauth.grok.failedToConvertSSO')
      appStore.showError(t('admin.accounts.oauth.batchFailed'))
    }
  } catch (error: any) {
    grokOAuth.error.value = error.response?.data?.detail || error.message || t('admin.accounts.oauth.grok.failedToConvertSSO')
    appStore.showError(grokOAuth.error.value)
  } finally {
    grokOAuth.loading.value = false
  }
}

/**
 * Grok password login: each line is email----password.
 * Password is only used for the authorize API call; buildCredentials never stores it.
 */
const handleGrokAuthorizePassword = async (emailPasswordInput: string) => {
  if (!emailPasswordInput.trim()) return
  if (!validateGrokOAuthUpstreamConfig()) return

  const lines = emailPasswordInput
    .split('\n')
    // Keep the password portion byte-for-byte; trim is only for determining
    // whether this textarea line is blank.
    .filter((line) => line.trim() && line.includes('----'))

  if (lines.length === 0) {
    grokOAuth.error.value = t(
      'admin.accounts.oauth.grok.pleaseEnterPassword',
      'Please enter email----password (one per line)'
    )
    return
  }

  grokOAuth.loading.value = true
  grokOAuth.error.value = ''

  let successCount = 0
  let failedCount = 0
  const errors: string[] = []

  try {
    for (let i = 0; i < lines.length; i++) {
      try {
        const tokenInfo = await grokOAuth.authorizePassword(lines[i], form.proxy_id)
        if (!tokenInfo) {
          failedCount++
          errors.push(`#${i + 1}: ${grokOAuth.error.value || 'Authorization failed'}`)
          grokOAuth.error.value = ''
          continue
        }

        const credentials = grokOAuth.buildCredentials(tokenInfo)
        applyGrokOAuthUpstreamConfig(credentials)
        const extra = grokOAuth.buildExtraInfo(tokenInfo)
        const accountName =
          lines.length > 1
            ? `${form.name || tokenInfo.email || 'Grok OAuth Account'} #${i + 1}`
            : form.name || tokenInfo.email || 'Grok OAuth Account'

        const modelMapping = buildModelMappingObject(
          modelRestrictionMode.value,
          allowedModels.value,
          modelMappings.value
        )
        if (modelMapping) {
          credentials.model_mapping = modelMapping
        }
        if (!applyTempUnschedConfig(credentials)) {
          return
        }

        await adminAPI.accounts.create({
          name: accountName,
          notes: form.notes,
          platform: 'grok',
          type: 'oauth',
          credentials,
          extra: withUpstreamRequestIdHeader(extra),
          proxy_id: form.proxy_id,
          concurrency: form.concurrency,
          load_factor: form.load_factor ?? undefined,
          priority: form.priority,
          rate_multiplier: form.rate_multiplier,
          group_ids: form.group_ids,
          expires_at: form.expires_at,
          auto_pause_on_expired: autoPauseOnExpired.value
        })
        successCount++
      } catch (error: any) {
        failedCount++
        const errMsg = error.response?.data?.detail || error.message || 'Unknown error'
        errors.push(`#${i + 1}: ${errMsg}`)
      }
    }

    if (successCount > 0 && failedCount === 0) {
      appStore.showSuccess(
        lines.length > 1
          ? t('admin.accounts.oauth.batchSuccess', { count: successCount })
          : t('admin.accounts.accountCreated')
      )
      emit('created')
      handleClose()
    } else if (successCount > 0) {
      appStore.showWarning(
        t('admin.accounts.oauth.batchPartialSuccess', {
          success: successCount,
          failed: failedCount
        })
      )
      grokOAuth.error.value = errors.join('\n')
      emit('created')
    } else {
      grokOAuth.error.value = errors.join('\n')
      appStore.showError(t('admin.accounts.oauth.batchFailed'))
    }
  } finally {
    grokOAuth.loading.value = false
  }
}

// OpenAI OAuth 授权码兑换
const handleOpenAIExchange = async (authCode: string) => {
  const oauthClient = openaiOAuth
  if (!authCode.trim() || !oauthClient.sessionId.value) return

  oauthClient.loading.value = true
  oauthClient.error.value = ''

  try {
    const stateToUse = (oauthFlowRef.value?.oauthState || oauthClient.oauthState.value || '').trim()
    if (!stateToUse) {
      oauthClient.error.value = t('admin.accounts.oauth.authFailed')
      appStore.showError(oauthClient.error.value)
      return
    }

    const tokenInfo = await oauthClient.exchangeAuthCode(
      authCode.trim(),
      oauthClient.sessionId.value,
      stateToUse,
      form.proxy_id
    )
    if (!tokenInfo) return

    const credentials = oauthClient.buildCredentials(tokenInfo)
    const oauthExtra = oauthClient.buildExtraInfo(tokenInfo) as Record<string, unknown> | undefined
    const extra = buildOpenAIExtra(oauthExtra)
    const shouldCreateOpenAI = form.platform === 'openai'

    // Add model mapping for OpenAI OAuth accounts（透传模式下不应用）
    if (shouldCreateOpenAI && !isOpenAIModelRestrictionDisabled.value) {
      const modelMapping = buildModelMappingObject(modelRestrictionMode.value, allowedModels.value, modelMappings.value)
      if (modelMapping) {
        credentials.model_mapping = modelMapping
      }
    }
    if (shouldCreateOpenAI) {
      const compactModelMapping = buildOpenAICompactModelMapping()
      if (compactModelMapping) {
        credentials.compact_model_mapping = compactModelMapping
      }
    }

    // 应用临时不可调度配置
    if (!applyTempUnschedConfig(credentials)) {
      return
    }

    if (shouldCreateOpenAI) {
      await adminAPI.accounts.create({
        name: form.name,
        notes: form.notes,
        platform: 'openai',
        type: 'oauth',
        credentials,
        extra: withUpstreamRequestIdHeader(extra),
        proxy_id: form.proxy_id,
        concurrency: form.concurrency,
        load_factor: form.load_factor ?? undefined,
        priority: form.priority,
        rate_multiplier: form.rate_multiplier,
        group_ids: form.group_ids,
        expires_at: form.expires_at,
        auto_pause_on_expired: autoPauseOnExpired.value
      })
      appStore.showSuccess(t('admin.accounts.accountCreated'))
    }

    emit('created')
    handleClose()
  } catch (error: any) {
    oauthClient.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
    appStore.showError(oauthClient.error.value)
  } finally {
    oauthClient.loading.value = false
  }
}

// OpenAI 手动 RT 批量验证和创建
// OpenAI Mobile RT client_id
const OPENAI_MOBILE_RT_CLIENT_ID = 'app_LlGpXReQgckcGGUo2JrYvtJK'

const buildOpenAICodexImportCredentialExtras = (): Record<string, unknown> | null => {
  const credentials: Record<string, unknown> = {}
  if (!isOpenAIModelRestrictionDisabled.value) {
    const modelMapping = buildModelMappingObject(modelRestrictionMode.value, allowedModels.value, modelMappings.value)
    if (modelMapping) {
      credentials.model_mapping = modelMapping
    }
  }

  const compactModelMapping = buildOpenAICompactModelMapping()
  if (compactModelMapping) {
    credentials.compact_model_mapping = compactModelMapping
  }

  if (!applyTempUnschedConfig(credentials)) {
    return null
  }
  return credentials
}

const formatCodexImportMessages = (messages?: CodexSessionImportMessage[]) => {
  return (messages || [])
    .map((item) => {
      const name = item.name ? ` ${item.name}` : ''
      return `#${item.index}${name}: ${item.message}`
    })
    .join('\n')
}

const isAgentIdentityImportContent = (content: string) => {
  const isAgentIdentityValue = (value: unknown): boolean => {
    if (Array.isArray(value)) return value.length > 0 && value.every(isAgentIdentityValue)
    if (!value || typeof value !== 'object') return false
    const record = value as Record<string, unknown>
    const authMode = record.auth_mode ?? record.authMode
    const agentIdentity = record.agent_identity ?? record.agentIdentity
    return (typeof authMode === 'string' && authMode.toLowerCase() === 'agentidentity')
      || (!!agentIdentity && typeof agentIdentity === 'object')
  }

  try {
    return isAgentIdentityValue(JSON.parse(content))
  } catch {
    const lines = content.split('\n').map((line) => line.trim()).filter(Boolean)
    if (lines.length === 0) return false
    try {
      return lines.every((line) => isAgentIdentityValue(JSON.parse(line)))
    } catch {
      return false
    }
  }
}

const handleOpenAIImportCodexSession = async (content: string) => {
  const oauthClient = openaiOAuth
  const trimmed = content.trim()
  if (!trimmed) {
    oauthClient.error.value = t('admin.accounts.oauth.openai.codexSessionEmpty')
    return
  }
  if (oauthFlowRef.value?.inputMethod === 'agent_identity' && !isAgentIdentityImportContent(trimmed)) {
    oauthClient.error.value = t('admin.accounts.oauth.openai.agentIdentityInvalid')
    return
  }

  const credentialExtras = buildOpenAICodexImportCredentialExtras()
  if (credentialExtras === null) {
    return
  }

  oauthClient.loading.value = true
  oauthClient.error.value = ''

  try {
    const extra = buildOpenAICodexImportExtra()
    const result = await adminAPI.accounts.importCodexSession({
      content: trimmed,
      name: form.name,
      notes: form.notes || null,
      proxy_id: form.proxy_id,
      concurrency: form.concurrency,
      load_factor: form.load_factor ?? undefined,
      priority: form.priority,
      rate_multiplier: form.rate_multiplier,
      group_ids: form.group_ids,
      expires_at: form.expires_at,
      auto_pause_on_expired: autoPauseOnExpired.value,
      credential_extras: Object.keys(credentialExtras).length > 0 ? credentialExtras : undefined,
      extra: withUpstreamRequestIdHeader(extra),
      update_existing: true
    })

    const successCount = result.created + result.updated
    const params = {
      created: result.created,
      updated: result.updated,
      skipped: result.skipped,
      failed: result.failed
    }

    if (successCount > 0 && result.failed === 0) {
      appStore.showSuccess(t('admin.accounts.oauth.openai.codexSessionImportSuccess', params))
      emit('created')
      handleClose()
      return
    }

    const errorText = formatCodexImportMessages(result.errors)
    const warningText = formatCodexImportMessages(result.warnings)
    oauthClient.error.value = [errorText, warningText].filter(Boolean).join('\n')

    if (result.failed === 0) {
      appStore.showWarning(t('admin.accounts.oauth.openai.codexSessionImportSuccess', params))
      return
    }

    if (successCount > 0) {
      appStore.showWarning(t('admin.accounts.oauth.openai.codexSessionImportPartial', params))
      emit('created')
      return
    }

    appStore.showError(t('admin.accounts.oauth.openai.codexSessionImportFailed'))
  } catch (error: any) {
    oauthClient.error.value =
      error.response?.data?.detail ||
      error.response?.data?.message ||
      error.message ||
      t('admin.accounts.oauth.openai.codexSessionImportFailed')
    appStore.showError(oauthClient.error.value)
  } finally {
    oauthClient.loading.value = false
  }
}

const handleOpenAIImportCodexPAT = async (accessToken: string) => {
  const oauthClient = openaiOAuth
  const trimmed = accessToken.trim()
  if (!trimmed) {
    oauthClient.error.value = t('admin.accounts.oauth.openai.codexPatEmpty')
    return
  }

  const credentialExtras = buildOpenAICodexImportCredentialExtras()
  if (credentialExtras === null) {
    return
  }

  oauthClient.loading.value = true
  oauthClient.error.value = ''

  try {
    const extra = buildOpenAICodexImportExtra()
    await adminAPI.accounts.createOpenAICodexPAT({
      access_token: trimmed,
      name: form.name,
      notes: form.notes || null,
      proxy_id: form.proxy_id,
      concurrency: form.concurrency,
      load_factor: form.load_factor ?? undefined,
      priority: form.priority,
      rate_multiplier: form.rate_multiplier,
      group_ids: form.group_ids,
      expires_at: form.expires_at,
      auto_pause_on_expired: autoPauseOnExpired.value,
      credential_extras: Object.keys(credentialExtras).length > 0 ? credentialExtras : undefined,
      extra: withUpstreamRequestIdHeader(extra)
    })

    appStore.showSuccess(t('admin.accounts.accountCreated'))
    emit('created')
    handleClose()
  } catch (error: any) {
    oauthClient.error.value =
      error.response?.data?.detail ||
      error.response?.data?.message ||
      error.message ||
      t('admin.accounts.oauth.openai.codexPatImportFailed')
    appStore.showError(oauthClient.error.value)
  } finally {
    oauthClient.loading.value = false
  }
}

// OpenAI RT 批量验证和创建（共享逻辑）
const handleOpenAIBatchRT = async (refreshTokenInput: string, clientId?: string) => {
  const oauthClient = openaiOAuth
  if (!refreshTokenInput.trim()) return

  const refreshTokens = refreshTokenInput
    .split('\n')
    .map((rt) => rt.trim())
    .filter((rt) => rt)

  if (refreshTokens.length === 0) {
    oauthClient.error.value = t('admin.accounts.oauth.openai.pleaseEnterRefreshToken')
    return
  }

  oauthClient.loading.value = true
  oauthClient.error.value = ''

  let successCount = 0
  let failedCount = 0
  const errors: string[] = []
  const shouldCreateOpenAI = form.platform === 'openai'

  try {
    for (let i = 0; i < refreshTokens.length; i++) {
      try {
        const tokenInfo = await oauthClient.validateRefreshToken(
          refreshTokens[i],
          form.proxy_id,
          clientId
        )
        if (!tokenInfo) {
          failedCount++
          errors.push(`#${i + 1}: ${oauthClient.error.value || 'Validation failed'}`)
          oauthClient.error.value = ''
          continue
        }

        const credentials = oauthClient.buildCredentials(tokenInfo)
        if (clientId) {
          credentials.client_id = clientId
        }
        const oauthExtra = oauthClient.buildExtraInfo(tokenInfo) as Record<string, unknown> | undefined
        const extra = buildOpenAIExtra(oauthExtra)

        // Add model mapping for OpenAI OAuth accounts（透传模式下不应用）
        if (shouldCreateOpenAI && !isOpenAIModelRestrictionDisabled.value) {
          const modelMapping = buildModelMappingObject(modelRestrictionMode.value, allowedModels.value, modelMappings.value)
          if (modelMapping) {
            credentials.model_mapping = modelMapping
          }
        }
        if (shouldCreateOpenAI) {
          const compactModelMapping = buildOpenAICompactModelMapping()
          if (compactModelMapping) {
            credentials.compact_model_mapping = compactModelMapping
          }
        }

        // Generate account name; fallback to email if name is empty (ent schema requires NotEmpty)
        const baseName = form.name || tokenInfo.email || 'OpenAI OAuth Account'
        const accountName = refreshTokens.length > 1 ? `${baseName} #${i + 1}` : baseName

        if (shouldCreateOpenAI) {
          await adminAPI.accounts.create({
            name: accountName,
            notes: form.notes,
            platform: 'openai',
            type: 'oauth',
            credentials,
            extra: withUpstreamRequestIdHeader(extra),
            proxy_id: form.proxy_id,
            concurrency: form.concurrency,
            load_factor: form.load_factor ?? undefined,
            priority: form.priority,
            rate_multiplier: form.rate_multiplier,
            group_ids: form.group_ids,
            expires_at: form.expires_at,
            auto_pause_on_expired: autoPauseOnExpired.value
          })
        }

        successCount++
      } catch (error: any) {
        failedCount++
        const errMsg = error.response?.data?.detail || error.message || 'Unknown error'
        errors.push(`#${i + 1}: ${errMsg}`)
      }
    }

    // Show results
    if (successCount > 0 && failedCount === 0) {
      appStore.showSuccess(
        refreshTokens.length > 1
          ? t('admin.accounts.oauth.batchSuccess', { count: successCount })
          : t('admin.accounts.accountCreated')
      )
      emit('created')
      handleClose()
    } else if (successCount > 0 && failedCount > 0) {
      appStore.showWarning(
        t('admin.accounts.oauth.batchPartialSuccess', { success: successCount, failed: failedCount })
      )
      oauthClient.error.value = errors.join('\n')
      emit('created')
    } else {
      oauthClient.error.value = errors.join('\n')
      appStore.showError(t('admin.accounts.oauth.batchFailed'))
    }
  } finally {
    oauthClient.loading.value = false
  }
}

// 手动输入 RT（Codex CLI client_id，默认）
const handleOpenAIValidateRT = (rt: string) => handleOpenAIBatchRT(rt)

// 手动输入 Mobile RT
const handleOpenAIValidateMobileRT = (rt: string) => handleOpenAIBatchRT(rt, OPENAI_MOBILE_RT_CLIENT_ID)

// Antigravity 手动 RT 批量验证和创建
const handleAntigravityValidateRT = async (refreshTokenInput: string) => {
  if (!refreshTokenInput.trim()) return

  // Parse multiple refresh tokens (one per line)
  const refreshTokens = refreshTokenInput
    .split('\n')
    .map((rt) => rt.trim())
    .filter((rt) => rt)

  if (refreshTokens.length === 0) {
    antigravityOAuth.error.value = t('admin.accounts.oauth.antigravity.pleaseEnterRefreshToken')
    return
  }

  antigravityOAuth.loading.value = true
  antigravityOAuth.error.value = ''

  let successCount = 0
  let failedCount = 0
  const errors: string[] = []

  try {
    for (let i = 0; i < refreshTokens.length; i++) {
      try {
        const tokenInfo = await antigravityOAuth.validateRefreshToken(
          refreshTokens[i],
          form.proxy_id
        )
        if (!tokenInfo) {
          failedCount++
          errors.push(`#${i + 1}: ${antigravityOAuth.error.value || 'Validation failed'}`)
          antigravityOAuth.error.value = ''
          continue
        }

        const credentials = antigravityOAuth.buildCredentials(tokenInfo, refreshTokens[i])
        applyAntigravityProjectID(credentials, antigravityProjectId.value, 'create')

        // Generate account name with index for batch
        const accountName = refreshTokens.length > 1 ? `${form.name} #${i + 1}` : form.name

        // Note: Antigravity doesn't have buildExtraInfo, so we pass empty extra or rely on credentials
        const createPayload = withAntigravityConfirmFlag({
          name: accountName,
          notes: form.notes,
          platform: 'antigravity',
          type: 'oauth',
          credentials,
          extra: withUpstreamRequestIdHeader({}),
          proxy_id: form.proxy_id,
          concurrency: form.concurrency,
          load_factor: form.load_factor ?? undefined,
          priority: form.priority,
          rate_multiplier: form.rate_multiplier,
          group_ids: form.group_ids,
          expires_at: form.expires_at,
          auto_pause_on_expired: autoPauseOnExpired.value
        })
        await adminAPI.accounts.create(createPayload)
        successCount++
      } catch (error: any) {
        failedCount++
        const errMsg = error.response?.data?.detail || error.message || 'Unknown error'
        errors.push(`#${i + 1}: ${errMsg}`)
      }
    }

    // Show results
    if (successCount > 0 && failedCount === 0) {
      appStore.showSuccess(
        refreshTokens.length > 1
          ? t('admin.accounts.oauth.batchSuccess', { count: successCount })
          : t('admin.accounts.accountCreated')
      )
      emit('created')
      handleClose()
    } else if (successCount > 0 && failedCount > 0) {
      appStore.showWarning(
        t('admin.accounts.oauth.batchPartialSuccess', { success: successCount, failed: failedCount })
      )
      antigravityOAuth.error.value = errors.join('\n')
      emit('created')
    } else {
      antigravityOAuth.error.value = errors.join('\n')
      appStore.showError(t('admin.accounts.oauth.batchFailed'))
    }
  } finally {
    antigravityOAuth.loading.value = false
  }
}

// Gemini OAuth 授权码兑换
const handleGeminiExchange = async (authCode: string) => {
  if (!authCode.trim() || !geminiOAuth.sessionId.value) return

  geminiOAuth.loading.value = true
  geminiOAuth.error.value = ''

  try {
    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || geminiOAuth.state.value
    if (!stateToUse) {
      geminiOAuth.error.value = t('admin.accounts.oauth.authFailed')
      appStore.showError(geminiOAuth.error.value)
      return
    }

    const tokenInfo = await geminiOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId: geminiOAuth.sessionId.value,
      state: stateToUse,
      proxyId: form.proxy_id,
      oauthType: geminiOAuthType.value,
      tierId: geminiSelectedTier.value
    })
    if (!tokenInfo) return

    const credentials = geminiOAuth.buildCredentials(tokenInfo)
    const extra = geminiOAuth.buildExtraInfo(tokenInfo)
    await createAccountAndFinish('gemini', 'oauth', credentials, extra)
  } catch (error: any) {
    geminiOAuth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
    appStore.showError(geminiOAuth.error.value)
  } finally {
    geminiOAuth.loading.value = false
  }
}

// Antigravity OAuth 授权码兑换
const handleAntigravityExchange = async (authCode: string) => {
  if (!authCode.trim() || !antigravityOAuth.sessionId.value) return

  antigravityOAuth.loading.value = true
  antigravityOAuth.error.value = ''

  try {
    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || antigravityOAuth.state.value
    if (!stateToUse) {
      antigravityOAuth.error.value = t('admin.accounts.oauth.authFailed')
      appStore.showError(antigravityOAuth.error.value)
      return
    }

    const tokenInfo = await antigravityOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId: antigravityOAuth.sessionId.value,
      state: stateToUse,
      proxyId: form.proxy_id
    })
		if (!tokenInfo) return

		const credentials = antigravityOAuth.buildCredentials(tokenInfo)
		applyAntigravityProjectID(credentials, antigravityProjectId.value, 'create')
		applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')
		// Antigravity 只使用映射模式
		const antigravityModelMapping = buildModelMappingObject(
			'mapping',
			[],
			antigravityModelMappings.value
		)
		if (antigravityModelMapping) {
			credentials.model_mapping = antigravityModelMapping
		}
		const extra = buildAntigravityExtra()
		await createAccountAndFinish('antigravity', 'oauth', credentials, extra)
  } catch (error: any) {
    antigravityOAuth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
    appStore.showError(antigravityOAuth.error.value)
  } finally {
    antigravityOAuth.loading.value = false
  }
}

// Grok OAuth 授权码兑换
const handleGrokExchange = async (authCode: string) => {
  if (!authCode.trim() || !grokOAuth.sessionId.value) return
  if (!validateGrokOAuthUpstreamConfig()) return

  grokOAuth.loading.value = true
  grokOAuth.error.value = ''

  try {
    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || grokOAuth.state.value
    if (!stateToUse) {
      grokOAuth.error.value = t('admin.accounts.oauth.authFailed')
      appStore.showError(grokOAuth.error.value)
      return
    }

    const tokenInfo = await grokOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId: grokOAuth.sessionId.value,
      state: stateToUse,
      proxyId: form.proxy_id
    })
    if (!tokenInfo) return

    const credentials = grokOAuth.buildCredentials(tokenInfo)
    applyGrokOAuthUpstreamConfig(credentials)
    const extra = grokOAuth.buildExtraInfo(tokenInfo)
    await createAccountAndFinish('grok', 'oauth', credentials, extra)
  } catch (error: any) {
    grokOAuth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
    appStore.showError(grokOAuth.error.value)
  } finally {
    grokOAuth.loading.value = false
  }
}

// Anthropic OAuth 授权码兑换
const handleAnthropicExchange = async (authCode: string) => {
  if (!authCode.trim() || !oauth.sessionId.value) return

  oauth.loading.value = true
  oauth.error.value = ''

  try {
    const proxyConfig = form.proxy_id ? { proxy_id: form.proxy_id } : {}
    const endpoint =
      addMethod.value === 'oauth'
        ? '/admin/accounts/exchange-code'
        : '/admin/accounts/exchange-setup-token-code'

    const tokenInfo = await adminAPI.accounts.exchangeCode(endpoint, {
      session_id: oauth.sessionId.value,
      code: authCode.trim(),
      ...proxyConfig
    })

    // Build extra with quota control settings
    const baseExtra = oauth.buildExtraInfo(tokenInfo) || {}
    const extra: Record<string, unknown> = { ...baseExtra }

    // Add window cost limit settings
    if (windowCostEnabled.value && windowCostLimit.value != null && windowCostLimit.value > 0) {
      extra.window_cost_limit = windowCostLimit.value
      extra.window_cost_sticky_reserve = windowCostStickyReserve.value ?? 10
    }

    // Add session limit settings
    if (sessionLimitEnabled.value && maxSessions.value != null && maxSessions.value > 0) {
      extra.max_sessions = maxSessions.value
      extra.session_idle_timeout_minutes = sessionIdleTimeout.value ?? 5
    }

    // Add RPM limit settings
    if (rpmLimitEnabled.value) {
      const DEFAULT_BASE_RPM = 15
      extra.base_rpm = (baseRpm.value != null && baseRpm.value > 0)
        ? baseRpm.value
        : DEFAULT_BASE_RPM
      extra.rpm_strategy = rpmStrategy.value
      if (rpmStickyBuffer.value != null && rpmStickyBuffer.value > 0) {
        extra.rpm_sticky_buffer = rpmStickyBuffer.value
      }
    }

    // UMQ mode（独立于 RPM）
    if (userMsgQueueMode.value) {
      extra.user_msg_queue_mode = userMsgQueueMode.value
    }

    // Add TLS fingerprint settings
    if (tlsFingerprintEnabled.value) {
      extra.enable_tls_fingerprint = true
      if (tlsFingerprintProfileId.value) {
        extra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
      }
    }

    // Add session ID masking settings
    if (sessionIdMaskingEnabled.value) {
      extra.session_id_masking_enabled = true
    }

    // Add cache TTL override settings
    if (cacheTTLOverrideEnabled.value) {
      extra.cache_ttl_override_enabled = true
      extra.cache_ttl_override_target = cacheTTLOverrideTarget.value
    }

    // Add custom base URL settings
    if (customBaseUrlEnabled.value && customBaseUrl.value.trim()) {
      extra.custom_base_url_enabled = true
      extra.custom_base_url = customBaseUrl.value.trim()
    }

    const credentials: Record<string, unknown> = { ...tokenInfo }
    applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')
    await createAccountAndFinish(form.platform, addMethod.value as AccountType, credentials, extra)
  } catch (error: any) {
    oauth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
    appStore.showError(oauth.error.value)
  } finally {
    oauth.loading.value = false
  }
}

// 主入口：根据平台路由到对应处理函数
const handleExchangeCode = async () => {
  const authCode = oauthFlowRef.value?.authCode || ''

  switch (form.platform) {
    case 'openai':
      return handleOpenAIExchange(authCode)
    case 'gemini':
      return handleGeminiExchange(authCode)
    case 'antigravity':
      return handleAntigravityExchange(authCode)
    case 'grok':
      return handleGrokExchange(authCode)
    default:
      return handleAnthropicExchange(authCode)
  }
}

const handleCookieAuth = async (sessionKey: string) => {
  oauth.loading.value = true
  oauth.error.value = ''

  try {
    const proxyConfig = form.proxy_id ? { proxy_id: form.proxy_id } : {}
    const keys = oauth.parseSessionKeys(sessionKey)

    if (keys.length === 0) {
      oauth.error.value = t('admin.accounts.oauth.pleaseEnterSessionKey')
      return
    }

    const tempUnschedPayload = tempUnschedEnabled.value
      ? buildTempUnschedRules(tempUnschedRules.value)
      : []
    if (tempUnschedEnabled.value && tempUnschedPayload.length === 0) {
      appStore.showError(t('admin.accounts.tempUnschedulable.rulesInvalid'))
      return
    }

    const endpoint =
      addMethod.value === 'oauth'
        ? '/admin/accounts/cookie-auth'
        : '/admin/accounts/setup-token-cookie-auth'

    let successCount = 0
    let failedCount = 0
    const errors: string[] = []

    for (let i = 0; i < keys.length; i++) {
      try {
        const tokenInfo = await adminAPI.accounts.exchangeCode(endpoint, {
          session_id: '',
          code: keys[i],
          ...proxyConfig
        })

        // Build extra with quota control settings
        const baseExtra = oauth.buildExtraInfo(tokenInfo) || {}
        const extra: Record<string, unknown> = { ...baseExtra }

        // Add window cost limit settings
        if (windowCostEnabled.value && windowCostLimit.value != null && windowCostLimit.value > 0) {
          extra.window_cost_limit = windowCostLimit.value
          extra.window_cost_sticky_reserve = windowCostStickyReserve.value ?? 10
        }

        // Add session limit settings
        if (sessionLimitEnabled.value && maxSessions.value != null && maxSessions.value > 0) {
          extra.max_sessions = maxSessions.value
          extra.session_idle_timeout_minutes = sessionIdleTimeout.value ?? 5
        }

        // Add RPM limit settings
        if (rpmLimitEnabled.value) {
          const DEFAULT_BASE_RPM = 15
          extra.base_rpm = (baseRpm.value != null && baseRpm.value > 0)
            ? baseRpm.value
            : DEFAULT_BASE_RPM
          extra.rpm_strategy = rpmStrategy.value
          if (rpmStickyBuffer.value != null && rpmStickyBuffer.value > 0) {
            extra.rpm_sticky_buffer = rpmStickyBuffer.value
          }
        }

        // UMQ mode（独立于 RPM）
        if (userMsgQueueMode.value) {
          extra.user_msg_queue_mode = userMsgQueueMode.value
        }

        // Add TLS fingerprint settings
        if (tlsFingerprintEnabled.value) {
          extra.enable_tls_fingerprint = true
          if (tlsFingerprintProfileId.value) {
            extra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
          }
        }

        // Add session ID masking settings
        if (sessionIdMaskingEnabled.value) {
          extra.session_id_masking_enabled = true
        }

        // Add cache TTL override settings
        if (cacheTTLOverrideEnabled.value) {
          extra.cache_ttl_override_enabled = true
          extra.cache_ttl_override_target = cacheTTLOverrideTarget.value
        }

        // Add custom base URL settings
        if (customBaseUrlEnabled.value && customBaseUrl.value.trim()) {
          extra.custom_base_url_enabled = true
          extra.custom_base_url = customBaseUrl.value.trim()
        }

        const accountName = keys.length > 1 ? `${form.name} #${i + 1}` : form.name

        const credentials: Record<string, unknown> = { ...tokenInfo }
        applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')
        if (tempUnschedEnabled.value) {
          credentials.temp_unschedulable_enabled = true
          credentials.temp_unschedulable_rules = tempUnschedPayload
        }

        await adminAPI.accounts.create({
          name: accountName,
          notes: form.notes,
          platform: form.platform,
          type: addMethod.value, // Use addMethod as type: 'oauth' or 'setup-token'
          credentials,
          extra: withUpstreamRequestIdHeader(extra),
          proxy_id: form.proxy_id,
          concurrency: form.concurrency,
          load_factor: form.load_factor ?? undefined,
          priority: form.priority,
          rate_multiplier: form.rate_multiplier,
          group_ids: form.group_ids,
          expires_at: form.expires_at,
          auto_pause_on_expired: autoPauseOnExpired.value
        })

        successCount++
      } catch (error: any) {
        failedCount++
        errors.push(
          t('admin.accounts.oauth.keyAuthFailed', {
            index: i + 1,
            error: error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
          })
        )
      }
    }

    if (successCount > 0) {
      appStore.showSuccess(t('admin.accounts.oauth.successCreated', { count: successCount }))
      if (failedCount === 0) {
        emit('created')
        handleClose()
      } else {
        emit('created')
      }
    }

    if (failedCount > 0) {
      oauth.error.value = errors.join('\n')
    }
  } catch (error: any) {
    oauth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.cookieAuthFailed')
  } finally {
    oauth.loading.value = false
  }
}
// ── Step flow (platform/type → credentials → models/schedule → limits/network → confirm) ──
// Every step stays mounted (v-show): fields, defaults and the request payload do not depend on
// the visible step. "Next" validates the current step; a native submit that hits an invalid field
// on a hidden step jumps to that step. While the onboarding tour runs, all steps are shown.
const WIZARD_STEP_KEYS = ['platform', 'credentials', 'modelsSchedule', 'limitsNetwork', 'confirm'] as const
const wizardStep = ref(0)
const wizardAvailable = ref<boolean[]>([true, true, true, true, true])
const wizardShowsAll = ref(false)
const createFormRef = ref<HTMLFormElement | null>(null)
let wizardCheckingSteps = false
let wizardInvalidJumpPending = false

function onboardingTourActive(): boolean {
  if (!getActivePinia()) return false
  try {
    return useOnboardingStore().isDriverActive()
  } catch {
    return false
  }
}
const showWizardStep = (index: number) => wizardShowsAll.value || wizardStep.value === index
const wizardSteps = computed(() =>
  WIZARD_STEP_KEYS.map((key, index) => ({ key, index })).filter((s) => wizardAvailable.value[s.index])
)
const wizardOnLastStep = computed(() => wizardStep.value === WIZARD_STEP_KEYS.length - 1)
const wizardStepperItems = computed(() => [
  ...wizardSteps.value.map((s) => ({ title: t(`admin.accounts.wizard.${s.key}`), value: s.index })),
  ...(isOAuthFlow.value ? [{ title: oauthStepTitle.value, value: WIZARD_STEP_KEYS.length, disabled: true }] : [])
])
const wizardStepperValue = computed(() => (step.value === 2 ? WIZARD_STEP_KEYS.length : wizardStep.value))
const wizardSection = (index: number) =>
  createFormRef.value?.querySelector<HTMLElement>(`[data-wizard-step="${index}"]`) ?? null

// A step is offered only when it renders something besides its section headings.
function refreshWizardSteps() {
  wizardShowsAll.value = onboardingTourActive()
  wizardAvailable.value = WIZARD_STEP_KEYS.map((key, index) => {
    if (key === 'confirm') return true
    const el = wizardSection(index)
    if (!el) return true
    return Array.from(el.children).some((child) => child.tagName !== 'H4')
  })
  if (!wizardAvailable.value[wizardStep.value]) {
    wizardStep.value = wizardSteps.value.find((s) => s.index > wizardStep.value)?.index ?? WIZARD_STEP_KEYS.length - 1
  }
}

function firstInvalidIn(index: number): HTMLElement | null {
  const el = wizardSection(index)
  if (!el) return null
  wizardCheckingSteps = true
  try {
    for (const control of Array.from(el.querySelectorAll<HTMLInputElement>('input, textarea, select'))) {
      if (!control.disabled && !control.checkValidity()) return control
    }
    return null
  } finally {
    wizardCheckingSteps = false
  }
}
function reportInvalid(control: HTMLElement | null) {
  if (!control) return
  wizardCheckingSteps = true
  try {
    ;(control as HTMLInputElement).reportValidity?.()
  } finally {
    wizardCheckingSteps = false
  }
}
function scrollDialogBodyTop(from: HTMLElement | null) {
  let node = from?.parentElement ?? null
  while (node) {
    const overflow = getComputedStyle(node).overflowY
    if (overflow === 'auto' || overflow === 'scroll') { node.scrollTop = 0; return }
    node = node.parentElement
  }
}

function goToWizardStep(target: number) {
  if (step.value !== 1 || target === wizardStep.value) return
  refreshWizardSteps()
  if (!wizardAvailable.value[target]) return
  if (target > wizardStep.value) {
    for (const s of wizardSteps.value) {
      if (s.index < wizardStep.value || s.index >= target) continue
      const invalid = firstInvalidIn(s.index)
      if (invalid) {
        wizardStep.value = s.index
        nextTick(() => reportInvalid(invalid))
        return
      }
    }
  }
  wizardStep.value = target
  nextTick(() => scrollDialogBodyTop(createFormRef.value))
}
function wizardNext() {
  const next = wizardSteps.value.find((s) => s.index > wizardStep.value)
  if (next) goToWizardStep(next.index)
}
function wizardBack() {
  const prev = [...wizardSteps.value].reverse().find((s) => s.index < wizardStep.value)
  if (prev) goToWizardStep(prev.index)
}
function onWizardStepperSelect(value: string | number | undefined) {
  const target = Number(value)
  if (!Number.isInteger(target) || target >= WIZARD_STEP_KEYS.length) return
  if (step.value === 2) {
    goBackToBasicInfo()
    nextTick(() => {
      wizardStep.value = target
      refreshWizardSteps()
    })
    return
  }
  goToWizardStep(target)
}
// Native constraint validation on submit: show the step that holds the first invalid field.
function onWizardInvalid(event: Event) {
  if (wizardCheckingSteps || wizardShowsAll.value || wizardInvalidJumpPending) return
  const section = (event.target as HTMLElement | null)?.closest<HTMLElement>('[data-wizard-step]')
  const index = section ? Number(section.dataset.wizardStep) : NaN
  if (!Number.isInteger(index) || index === wizardStep.value) return
  wizardInvalidJumpPending = true
  wizardStep.value = index
  nextTick(() => {
    reportInvalid(event.target as HTMLElement)
    wizardInvalidJumpPending = false
  })
}

// Confirm step: a short read-only summary of what will be created.
const wizardSummary = computed(() => {
  if (wizardStep.value !== WIZARD_STEP_KEYS.length - 1 && !wizardShowsAll.value) return []
  const picked = (createFormRef.value
    ? Array.from(createFormRef.value.querySelectorAll<HTMLElement>('[data-wizard-step="0"] [aria-pressed="true"]'))
    : []
  ).map((el) => el.querySelector('[data-slot="title"]')?.textContent?.trim() || el.textContent?.trim() || '')
  const rows = [
    { label: t('admin.accounts.wizard.summaryPlatform'), value: picked.filter(Boolean).join(' · ') || form.platform },
    { label: t('admin.accounts.accountName'), value: form.name || '—' },
    { label: t('admin.accounts.concurrency'), value: String(form.concurrency) },
    { label: t('admin.accounts.priority'), value: String(form.priority) },
    { label: t('admin.accounts.wizard.summaryGroups'), value: String(form.group_ids.length) },
    { label: t('admin.accounts.expiresAt'), value: expiresAtInput.value || '—' }
  ]
  return rows
})

watch(
  () => [form.platform, form.type, accountCategory.value, addMethod.value, antigravityAccountType.value, accountMode.value],
  () => nextTick(refreshWizardSteps)
)
watch(createFormRef, (el) => { if (el) nextTick(refreshWizardSteps) })
watch(
  () => props.show,
  (open) => { if (open) wizardStep.value = 0 }
)
</script>
