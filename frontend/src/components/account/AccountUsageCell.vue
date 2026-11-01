<template>
  <div ref="rootRef" v-if="showUsageWindows">
    <!-- Anthropic OAuth and Setup Token accounts: fetch real usage data -->
    <template
      v-if="
        account.platform === 'anthropic' &&
        (account.type === 'oauth' || account.type === 'setup-token')
      "
    >
      <!-- Loading state -->
      <div v-if="loading" class="space-y-1.5">
        <!-- OAuth: 3 rows, Setup Token: 1 row -->
        <div class="flex items-center gap-1">
          <USkeleton class="h-3 w-[32px]" />
          <USkeleton class="h-1.5 w-8 rounded-full" />
          <USkeleton class="h-3 w-[32px]" />
        </div>
        <template v-if="account.type === 'oauth'">
          <div class="flex items-center gap-1">
            <USkeleton class="h-3 w-[32px]" />
            <USkeleton class="h-1.5 w-8 rounded-full" />
            <USkeleton class="h-3 w-[32px]" />
          </div>
          <div class="flex items-center gap-1">
            <USkeleton class="h-3 w-[32px]" />
            <USkeleton class="h-1.5 w-8 rounded-full" />
            <USkeleton class="h-3 w-[32px]" />
          </div>
        </template>
      </div>

      <!-- Error state -->
      <div v-else-if="error" class="text-xs text-error">
        {{ error }}
      </div>

      <!-- Usage data -->
      <div v-else-if="usageInfo" class="space-y-1">
        <!-- API error (degraded response) -->
        <div v-if="usageInfo.error" class="max-w-[200px] truncate text-xs text-warning" :title="usageInfo.error">
          {{ usageInfo.error }}
        </div>
        <!-- 5h Window -->
        <UsageProgressBar
          v-if="usageInfo.five_hour"
          label="5h"
          :utilization="usageInfo.five_hour.utilization"
          :resets-at="usageInfo.five_hour.resets_at"
          :window-stats="usageInfo.five_hour.window_stats"
          color="indigo"
        />

        <!-- 7d Window (OAuth only) -->
        <UsageProgressBar
          v-if="usageInfo.seven_day"
          label="7d"
          :utilization="usageInfo.seven_day.utilization"
          :resets-at="usageInfo.seven_day.resets_at"
          color="emerald"
        />

        <!-- 7d Sonnet Window (OAuth only) -->
        <UsageProgressBar
          v-if="usageInfo.seven_day_sonnet"
          label="7d S"
          :utilization="usageInfo.seven_day_sonnet.utilization"
          :resets-at="usageInfo.seven_day_sonnet.resets_at"
          color="purple"
        />

        <!-- 7d Fable Window (7d_oi) -->
        <UsageProgressBar
          v-if="usageInfo.seven_day_fable"
          label="7d F"
          :utilization="usageInfo.seven_day_fable.utilization"
          :resets-at="usageInfo.seven_day_fable.resets_at"
          color="amber"
        />

      </div>

      <!-- No data yet -->
      <div v-else class="space-y-1">
        <div class="text-xs text-dimmed">-</div>
      </div>
      <!--
        One stable instance for every usage state, so a reset-credit query started
        while usage is still loading survives the usage response. The local query
        button shares its row once usage data exists.
      -->
      <ClaudeResetCreditsCell :account="account" class="mt-1" @redeemed="loadActiveUsage">
        <template v-if="usageInfo" #pre-actions>
          <span
            v-if="usageInfo.source === 'passive'"
            class="text-xs text-dimmed"
          >
            {{ t('admin.accounts.usageWindow.passiveSampled') }}
          </span>
          <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-refresh-cw" class="px-1.5" :loading="activeQueryLoading" :disabled="activeQueryLoading" :label="t('admin.accounts.usageWindow.activeQuery')" @click="loadActiveUsage" />
        </template>
      </ClaudeResetCreditsCell>
    </template>

    <!-- OpenAI Codex accounts: ticket status; usage querying remains OAuth-only. -->
    <template v-else-if="account.platform === 'openai' && (account.type === 'oauth' || account.type === 'setup-token')">
      <div v-if="codexTurnTickets.length" class="mb-1 space-y-0.5">
        <div
          v-for="ticket in codexTurnTickets"
          :key="ticket.model"
          class="flex items-center gap-1 text-xs leading-4"
        >
          <span class="truncate font-medium text-muted" :title="ticket.model">{{ shortCodexTicketModel(ticket.model) }}</span>
          <span v-if="ticket.ready" class="text-success">{{ formatCodexTicketRemaining(ticket.remaining_seconds) }}</span>
          <span v-else-if="ticket.blocked" class="text-warning">{{ t('admin.accounts.openai.codexTurnTicketPaused') }}</span>
          <span v-else class="text-muted">{{ t('admin.accounts.openai.codexTurnTicketMissing') }}</span>
        </div>
      </div>
      <div v-if="hasOpenAIUsageFallback" class="space-y-1">
        <UsageProgressBar
          v-if="usageInfo?.five_hour"
          label="5h"
          :utilization="usageInfo.five_hour.utilization"
          :resets-at="usageInfo.five_hour.resets_at"
          :window-stats="usageInfo.five_hour.window_stats"
          :show-now-when-idle="true"
          color="indigo"
        />
        <UsageProgressBar
          v-if="usageInfo?.seven_day"
          label="7d"
          :utilization="usageInfo.seven_day.utilization"
          :resets-at="usageInfo.seven_day.resets_at"
          :window-stats="usageInfo.seven_day.window_stats"
          :estimated-total-cost="openAISevenDayEstimatedTotalCost"
          :show-now-when-idle="true"
          color="emerald"
        />
        <!--
          Upstream codex /wham/usage quota query + reset. The local active-sampling
          refresh button is rendered via the pre-actions slot so the user sees a
          single row of related buttons instead of two stacked rows.
        -->
        <OpenAIQuotaResetCell :account="account" @account-updated="handleQuotaResetAccountUpdated">
          <template #pre-actions>
            <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-refresh-cw" class="px-1.5" :loading="activeQueryLoading" :disabled="activeQueryLoading" :label="t('admin.accounts.usageWindow.activeQuery')" @click="loadActiveUsage" />
          </template>
        </OpenAIQuotaResetCell>
      </div>
      <div v-else-if="loading" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <USkeleton class="h-3 w-[32px]" />
          <USkeleton class="h-1.5 w-8 rounded-full" />
          <USkeleton class="h-3 w-[32px]" />
        </div>
        <div class="flex items-center gap-1">
          <USkeleton class="h-3 w-[32px]" />
          <USkeleton class="h-1.5 w-8 rounded-full" />
          <USkeleton class="h-3 w-[32px]" />
        </div>
      </div>
      <div v-else>
        <div class="text-xs text-dimmed">-</div>
        <!-- Always allow on-demand upstream quota query, even before local data exists. -->
        <OpenAIQuotaResetCell
          v-if="account.type === 'oauth'"
          :account="account"
          class="mt-1"
          @account-updated="handleQuotaResetAccountUpdated"
        />
      </div>
    </template>

    <!-- Antigravity OAuth accounts: fetch usage from API -->
    <template v-else-if="account.platform === 'antigravity' && account.type === 'oauth'">
      <!-- 账户类型徽章 -->
      <div v-if="antigravityTierLabel" class="mb-1 flex items-center gap-1">
        <UBadge :color="antigravityTierColor" variant="subtle" size="sm">{{ antigravityTierLabel }}</UBadge>
        <!-- 不合格账户警告图标 -->
        <UPopover v-if="hasIneligibleTiers" mode="hover" :content="{ side: 'bottom', align: 'start' }">
          <span tabindex="0" class="inline-flex cursor-help text-error" :aria-label="t('admin.accounts.ineligibleWarning')">
            <UIcon name="i-lucide-circle-alert" class="size-3.5" />
          </span>
          <template #content>
            <p class="w-80 px-3 py-2 text-xs leading-relaxed text-default">{{ t('admin.accounts.ineligibleWarning') }}</p>
          </template>
        </UPopover>
      </div>

      <!-- Forbidden state (403) -->
      <div v-if="isForbidden" class="space-y-1">
        <UBadge :color="forbiddenBadgeColor" variant="subtle" size="sm">{{ forbiddenLabel }}</UBadge>
        <div v-if="validationURL" class="flex items-center gap-1">
          <a
            :href="validationURL"
            target="_blank"
            rel="noopener noreferrer"
            class="text-xs text-primary hover:underline"
            :title="t('admin.accounts.openVerification')"
          >
            {{ t('admin.accounts.openVerification') }}
          </a>
          <UButton color="neutral" variant="link" size="xs" class="p-0" :title="t('admin.accounts.copyLink')" :label="linkCopied ? t('admin.accounts.linkCopied') : t('admin.accounts.copyLink')" @click="copyValidationURL" />
        </div>
      </div>

      <!-- Needs reauth (401) -->
      <div v-else-if="needsReauth" class="space-y-1">
        <UBadge color="warning" variant="subtle" size="sm">{{ t('admin.accounts.needsReauth') }}</UBadge>
      </div>

      <!-- Degraded error (non-403, non-401) -->
      <div v-else-if="usageInfo?.error" class="space-y-1">
        <UBadge color="warning" variant="subtle" size="sm">{{ usageErrorLabel }}</UBadge>
      </div>

      <!-- Loading state -->
      <div v-else-if="loading" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <USkeleton class="h-3 w-[32px]" />
          <USkeleton class="h-1.5 w-8 rounded-full" />
          <USkeleton class="h-3 w-[32px]" />
        </div>
      </div>

      <!-- Error state -->
      <div v-else-if="error" class="text-xs text-error">
        {{ error }}
      </div>

      <!-- Usage data from API -->
      <div v-else-if="hasAntigravityQuotaFromAPI" class="space-y-1">
        <!-- Gemini 3 Pro -->
        <UsageProgressBar
          v-if="antigravity3ProUsageFromAPI !== null"
          :label="t('admin.accounts.usageWindow.gemini3Pro')"
          :utilization="antigravity3ProUsageFromAPI.utilization"
          :resets-at="antigravity3ProUsageFromAPI.resetTime"
          color="indigo"
        />

        <!-- Gemini 3 Flash -->
        <UsageProgressBar
          v-if="antigravity3FlashUsageFromAPI !== null"
          :label="t('admin.accounts.usageWindow.gemini3Flash')"
          :utilization="antigravity3FlashUsageFromAPI.utilization"
          :resets-at="antigravity3FlashUsageFromAPI.resetTime"
          color="emerald"
        />

        <!-- Gemini 3 Image -->
        <UsageProgressBar
          v-if="antigravity3ImageUsageFromAPI !== null"
          :label="t('admin.accounts.usageWindow.gemini3Image')"
          :utilization="antigravity3ImageUsageFromAPI.utilization"
          :resets-at="antigravity3ImageUsageFromAPI.resetTime"
          color="purple"
        />

        <!-- Claude -->
        <UsageProgressBar
          v-if="antigravityClaudeUsageFromAPI !== null"
          :label="t('admin.accounts.usageWindow.claude')"
          :utilization="antigravityClaudeUsageFromAPI.utilization"
          :resets-at="antigravityClaudeUsageFromAPI.resetTime"
          color="amber"
        />

        <div v-if="aiCreditsDisplay" class="mt-1 text-xs text-muted">
          <UIcon name="i-lucide-credit-card" class="me-1 inline size-3 align-[-2px] text-dimmed" />{{ t('admin.accounts.aiCreditsBalance') }}: {{ aiCreditsDisplay }}
        </div>
      </div>
      <div v-else-if="aiCreditsDisplay" class="text-xs text-muted">
        <UIcon name="i-lucide-credit-card" class="me-1 inline size-3 align-[-2px] text-dimmed" />{{ t('admin.accounts.aiCreditsBalance') }}: {{ aiCreditsDisplay }}
      </div>
      <div v-else class="text-xs text-dimmed">-</div>
    </template>

    <!-- Grok OAuth accounts: passive xAI quota headers + local Sub2API usage -->
    <template v-else-if="account.platform === 'grok' && account.type === 'oauth'">
      <div v-if="loading" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <USkeleton class="h-3 w-[32px]" />
          <USkeleton class="h-1.5 w-8 rounded-full" />
          <USkeleton class="h-3 w-[32px]" />
        </div>
      </div>
      <div v-else-if="error" class="text-xs text-error">
        {{ error }}
      </div>
      <div v-else-if="needsReauth" class="space-y-1">
        <UBadge color="warning" variant="subtle" size="sm">{{ t('admin.accounts.needsReauth') }}</UBadge>
      </div>
      <div v-else-if="isForbidden" class="space-y-1">
        <UBadge color="error" variant="subtle" size="sm">{{ usageInfo?.grok_entitlement_status || t('admin.accounts.forbidden') }}</UBadge>
      </div>
      <div v-else-if="usageInfo" class="space-y-1">
        <!-- Free: only rolling 24h soft-gate bar. Paid: 7d + 30d + prepaid money. -->
        <template v-if="grokIsFree">
          <UsageProgressBar
            v-if="grokFreeTokenBar"
            label="24h"
            :title="t('admin.accounts.usageWindow.grokFreeQuota24hHint', { limit: formatCompactNumber(grokFreeTokenBar.limit) })"
            :utilization="grokFreeTokenBar.utilization"
            :window-stats="grokFreeQuotaUsage"
            :show-now-when-idle="true"
            color="emerald"
          />
          <div v-else-if="grokQuotaUnknown" class="text-xs text-muted">
            {{ grokQuotaUnknownLabel }}
          </div>
        </template>
        <template v-else>
          <UsageProgressBar
            v-if="grokWeeklyBillingBar"
            label="7d"
            :utilization="grokWeeklyBillingBar.utilization"
            :resets-at="grokWeeklyBillingBar.resetsAt"
            :window-stats="grokWeeklyBillingBar.windowStats"
            :show-now-when-idle="true"
            color="indigo"
          />
          <UsageProgressBar
            v-if="grokMonthlyBillingBar"
            label="30d"
            :utilization="grokMonthlyBillingBar.utilization"
            :resets-at="grokMonthlyBillingBar.resetsAt"
            :window-stats="grokMonthlyBillingBar.windowStats"
            :show-now-when-idle="true"
            color="indigo"
          />
          <div
            v-if="grokPrepaidMoneyLine"
            class="flex flex-wrap items-center gap-1 text-xs text-muted"
          >
            <span
              v-if="grokPrepaidMoneyLine.showPrepaid"
              class="text-success"
              :title="t('admin.accounts.usageWindow.grokPrepaid')"
            >
              {{ t('admin.accounts.usageWindow.grokPrepaid') }} ${{ grokPrepaidMoneyLine.prepaid }}
            </span>
            <span
              v-if="grokPrepaidMoneyLine.showUsedLimit"
              :title="t('admin.accounts.usageWindow.grokMonthlyLimit')"
            >
              {{ t('admin.accounts.usageWindow.grokUsed') }}
              {{ grokPrepaidMoneyLine.used }}/{{ grokPrepaidMoneyLine.limit }}
            </span>
          </div>
          <div v-if="grokQuotaUnknown" class="text-xs text-muted">
            {{ grokQuotaUnknownLabel }}
          </div>
        </template>
        <div v-if="usageInfo.error" class="max-w-[200px] truncate text-xs text-warning" :title="usageInfo.error">
          {{ usageErrorLabel }}
        </div>
        <div v-if="grokRetryAfterLabel" class="text-xs text-warning">
          {{ t('admin.accounts.usageWindow.grokRetryAfter', { time: grokRetryAfterLabel }) }}
        </div>
        <GrokQuotaProbeCell :account="account" compact @probed="handleGrokProbed" />
      </div>
      <div v-else class="space-y-1">
        <div class="text-xs text-dimmed">-</div>
        <GrokQuotaProbeCell :account="account" compact @probed="handleGrokProbed" />
      </div>
    </template>

    <!-- CN providers (Kimi / Zhipu / DeepSeek): coding-plan quota or payg balance -->
    <template v-else-if="account.platform === 'kimi' || account.platform === 'zhipu' || account.platform === 'deepseek' || account.platform === 'minimax'">
      <!-- 挂在 CN 平台下的 Ollama Cloud 账号（资格由后端下发 eligible）：用量由
           Ollama 用量窗口负责。这类账号不是国产厂商订阅，CN 的额度/余额探测端点由
           base_url 衍生，对 ollama.com 会被后端出站 URL 白名单拒绝，渲染出来只会
           给用户一行探测报错，因此不再渲染 CN 子单元格与占位符。 -->
      <OllamaCloudUsageCell
        v-if="account.ollama_cloud_usage?.eligible"
        :account="account"
        @updated="handleOllamaCloudUsageUpdated"
      />
      <div v-else class="space-y-1">
        <!-- 子单元格各自按 模式×平台 判定可见；两者都不可见时（智谱 payg 无公开
             余额端点、coding 探测也不适用）才回落到占位符。 -->
        <div
          v-if="!cnQuotaCellVisible && !cnBalanceCellVisible"
          class="text-xs text-dimmed"
          :title="t('admin.accounts.cnProviders.noBalanceEndpoint')"
        >-</div>
        <CNProviderQuotaCell :account="account" />
        <CNProviderBalanceCell :account="account" />
      </div>
    </template>

    <!-- Gemini platform: show quota + local usage window -->
    <template v-else-if="account.platform === 'gemini'">
      <!-- Auth Type + Tier Badge (first line) -->
      <div v-if="geminiAuthTypeLabel" class="mb-1 flex items-center gap-1">
        <UBadge :color="geminiTierColor" variant="subtle" size="sm">{{ geminiAuthTypeLabel }}</UBadge>
        <!-- Quota policy on hover / focus -->
        <UPopover mode="hover" :content="{ side: 'bottom', align: 'start' }">
          <span tabindex="0" class="inline-flex cursor-help text-dimmed hover:text-muted" :aria-label="t('admin.accounts.gemini.quotaPolicy.title')">
            <UIcon name="i-lucide-circle-help" class="size-3.5" />
          </span>
          <template #content>
            <div class="w-80 space-y-1 px-3 py-2 text-xs leading-relaxed text-default">
              <div class="font-medium text-highlighted">{{ t('admin.accounts.gemini.quotaPolicy.title') }}</div>
              <div class="text-muted">{{ t('admin.accounts.gemini.quotaPolicy.note') }}</div>
              <div class="pt-1 font-medium">{{ geminiQuotaPolicyChannel }}:</div>
              <div class="pl-2">• {{ geminiQuotaPolicyLimits }}</div>
              <a :href="geminiQuotaPolicyDocsUrl" target="_blank" rel="noopener noreferrer" class="inline-block pt-1 text-primary hover:underline">
                {{ t('admin.accounts.gemini.quotaPolicy.columns.docs') }} →
              </a>
            </div>
          </template>
        </UPopover>
      </div>

      <!-- Usage data or unlimited flow -->
      <div class="space-y-1">
        <div
          v-if="showGeminiTodayStats && todayStats"
          class="mb-0.5 flex items-center"
        >
          <div class="flex flex-wrap items-center gap-x-2 text-xs tabular-nums text-muted">
            <span>
              {{ formatKeyRequests }} req
            </span>
            <span>
              {{ formatKeyTokens }}
            </span>
            <span :title="t('usage.accountBilled')">
              A ${{ formatKeyCost }}
            </span>
            <span
              v-if="todayStats.user_cost != null"
              :title="t('usage.userBilled')"
            >
              U ${{ formatKeyUserCost }}
            </span>
          </div>
        </div>
        <div
          v-else-if="showGeminiTodayStats && todayStatsLoading"
          class="mb-0.5 flex items-center gap-1"
        >
          <USkeleton class="h-3 w-10" />
          <USkeleton class="h-3 w-8" />
          <USkeleton class="h-3 w-12" />
        </div>
        <div v-if="loading" class="space-y-1">
          <div class="flex items-center gap-1">
            <USkeleton class="h-3 w-[32px]" />
            <USkeleton class="h-1.5 w-8 rounded-full" />
            <USkeleton class="h-3 w-[32px]" />
          </div>
        </div>
        <div v-else-if="error" class="text-xs text-error">
          {{ error }}
        </div>
        <!-- Gemini: show daily usage bars when available -->
        <div v-else-if="geminiUsageAvailable" class="space-y-1">
          <UsageProgressBar
            v-for="bar in geminiUsageBars"
            :key="bar.key"
            :label="bar.label"
            :utilization="bar.utilization"
            :resets-at="bar.resetsAt"
            :window-stats="bar.windowStats"
            :color="bar.color"
          />
          <p class="mt-1 text-xs text-dimmed">
            * {{ t('admin.accounts.gemini.quotaPolicy.simulatedNote') || 'Simulated quota' }}
          </p>
        </div>
        <!-- AI Studio Client OAuth: show unlimited flow (no usage tracking) -->
        <div v-else class="text-xs text-dimmed">
          {{ t('admin.accounts.gemini.rateLimit.unlimited') }}
        </div>
      </div>
    </template>

    <!-- Other accounts: no usage window -->
    <template v-else>
      <div class="text-xs text-dimmed">-</div>
    </template>
  </div>

  <!-- Non-OAuth/Setup-Token accounts -->
  <div ref="rootRef" v-else>
    <!-- Gemini API Key accounts: show quota info -->
    <AccountQuotaInfo v-if="account.platform === 'gemini'" :account="account" />
    <!-- Key/Bedrock accounts: show today stats + optional quota bars -->
    <div v-else class="space-y-1">
      <OllamaCloudUsageCell
        v-if="account.ollama_cloud_usage?.eligible"
        :account="account"
        @updated="handleOllamaCloudUsageUpdated"
      />
      <!-- Today stats row (requests, tokens, cost, user_cost) -->
      <div
        v-if="todayStats"
        class="mb-0.5 flex items-center"
      >
        <div class="flex flex-wrap items-center gap-x-2 text-xs tabular-nums text-muted">
          <span>
            {{ formatKeyRequests }} req
          </span>
          <span>
            {{ formatKeyTokens }}
          </span>
          <span :title="t('usage.accountBilled')">
            A ${{ formatKeyCost }}
          </span>
          <span
            v-if="todayStats.user_cost != null"
            :title="t('usage.userBilled')"
          >
            U ${{ formatKeyUserCost }}
          </span>
        </div>
      </div>
      <!-- Loading skeleton for today stats -->
      <div
        v-else-if="todayStatsLoading"
        class="mb-0.5 flex items-center gap-1"
      >
        <USkeleton class="h-3 w-10" />
        <USkeleton class="h-3 w-8" />
        <USkeleton class="h-3 w-12" />
      </div>

      <!-- API Key accounts with quota limits: show progress bars -->
      <UsageProgressBar
        v-if="quotaDailyBar"
        label="1d"
        :utilization="quotaDailyBar.utilization"
        :resets-at="quotaDailyBar.resetsAt"
        color="indigo"
      />
      <UsageProgressBar
        v-if="quotaWeeklyBar"
        label="7d"
        :utilization="quotaWeeklyBar.utilization"
        :resets-at="quotaWeeklyBar.resetsAt"
        color="emerald"
      />
      <UsageProgressBar
        v-if="quotaTotalBar"
        label="total"
        :utilization="quotaTotalBar.utilization"
        color="purple"
      />

      <!-- No data at all -->
      <div
        v-if="!todayStats && !todayStatsLoading && !hasApiKeyQuota && !account.ollama_cloud_usage?.eligible"
        class="text-xs text-dimmed"
      >-</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, onUnmounted, watch } from 'vue'
import type { BadgeProps } from '@nuxt/ui'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { Account, AccountUsageInfo, GeminiCredentials, WindowStats } from '@/types'
import { buildOpenAIUsageRefreshKey } from '@/utils/accountUsageRefresh'
import { enqueueUsageRequest } from '@/utils/usageLoadQueue'
import { formatCompactNumber } from '@/utils/format'
import UsageProgressBar from './UsageProgressBar.vue'
import AccountQuotaInfo from './AccountQuotaInfo.vue'
import ClaudeResetCreditsCell from './ClaudeResetCreditsCell.vue'
import OpenAIQuotaResetCell from './OpenAIQuotaResetCell.vue'
import GrokQuotaProbeCell from './GrokQuotaProbeCell.vue'
import CNProviderQuotaCell from './CNProviderQuotaCell.vue'
import CNProviderBalanceCell from './CNProviderBalanceCell.vue'
import OllamaCloudUsageCell from './OllamaCloudUsageCell.vue'
import { cnQuotaCellVisible as cnQuotaCellVisibleFn, cnBalanceCellVisible as cnBalanceCellVisibleFn, isGeminiOpenAIProtocolAccount } from './credentialsBuilder'

// Module-level cache shared across all AccountUsageCell instances
const _usageCache = new Map<number, { data: AccountUsageInfo; ts: number }>()
const USAGE_CACHE_TTL = 5 * 60 * 1000 // 5 minutes

const props = withDefaults(
  defineProps<{
    account: Account
    todayStats?: WindowStats | null
    todayStatsLoading?: boolean
    manualRefreshToken?: number
    batchedUsage?: AccountUsageInfo | null
    batchedUsageError?: string | null
    batchedUsageLoading?: boolean
    requestBatchedUsage?: ((account: Account, options?: { force?: boolean }) => void) | null
  }>(),
  {
    todayStats: null,
    todayStatsLoading: false,
    manualRefreshToken: 0,
    batchedUsage: null,
    batchedUsageError: null,
    batchedUsageLoading: false,
    requestBatchedUsage: null
  }
)

const emit = defineEmits<{
  'account-updated': [account: Account]
  'usage-loaded': [usage: AccountUsageInfo]
}>()

const { t } = useI18n()

type BadgeColor = NonNullable<BadgeProps['color']>
const desktopViewportQuery = '(min-width: 768px)'

const unmounted = ref(false)
onBeforeUnmount(() => { unmounted.value = true })

const loading = ref(false)
const activeQueryLoading = ref(false)
const error = ref<string | null>(null)
const usageInfo = ref<AccountUsageInfo | null>(null)
watch(usageInfo, (usage) => {
  if (usage) emit('usage-loaded', usage)
})
const rootRef = ref<HTMLElement | null>(null)
const isDesktopViewport = ref(
  typeof window === 'undefined' ? true : window.matchMedia(desktopViewportQuery).matches
)
const hasEnteredViewport = ref(false)
const pendingAutoLoad = ref(false)
const pendingAutoLoadSource = ref<'passive' | 'active' | undefined>(undefined)

let desktopViewportMediaQuery: MediaQueryList | null = null
let desktopViewportListener: ((event: MediaQueryListEvent) => void) | null = null
let visibilityObserver: IntersectionObserver | null = null

// Show usage windows for OAuth and Setup Token accounts
const showUsageWindows = computed(() => {
  // Gemini: we can always compute local usage windows from DB logs (simulated quotas).
  if (props.account.platform === 'gemini') return true
  // CN providers: apikey 账号也有滚动用量窗口（coding plan）或余额（payg），
  // 由 CNProviderQuotaCell / CNProviderBalanceCell 自行探测与展示。
  if (
    props.account.platform === 'kimi' ||
    props.account.platform === 'zhipu' ||
    props.account.platform === 'deepseek' ||
    props.account.platform === 'minimax'
  ) {
    return true
  }
  return props.account.type === 'oauth' || props.account.type === 'setup-token'
})

const shouldFetchUsage = computed(() => {
  if (props.account.platform === 'anthropic') {
    return props.account.type === 'oauth' || props.account.type === 'setup-token'
  }
  if (props.account.platform === 'gemini') {
    return true
  }
  if (props.account.platform === 'antigravity') {
    return props.account.type === 'oauth'
  }
  if (props.account.platform === 'grok') {
    return props.account.type === 'oauth'
  }
  if (props.account.platform === 'openai') {
    return props.account.type === 'oauth'
  }
  return false
})

// CN 供应商子单元格可见性（与 CNProviderQuotaCell / CNProviderBalanceCell 共用
// credentialsBuilder 的单一实现）：都不可见时显示 `-` 占位符。
const cnAccountMode = computed(() => {
  const mode = props.account.credentials?.account_mode
  return typeof mode === 'string' ? mode : ''
})
const cnQuotaCellVisible = computed(() => cnQuotaCellVisibleFn(props.account.platform, cnAccountMode.value))
const cnBalanceCellVisible = computed(() => cnBalanceCellVisibleFn(props.account.platform, cnAccountMode.value))

const isBatchManaged = computed(() => typeof props.requestBatchedUsage === 'function')

const showGeminiTodayStats = computed(() => {
  return props.account.platform === 'gemini' && props.account.type === 'service_account'
})

const geminiUsageAvailable = computed(() => {
  return (
    !!usageInfo.value?.gemini_shared_daily ||
    !!usageInfo.value?.gemini_pro_daily ||
    !!usageInfo.value?.gemini_flash_daily ||
    !!usageInfo.value?.gemini_shared_minute ||
    !!usageInfo.value?.gemini_pro_minute ||
    !!usageInfo.value?.gemini_flash_minute
  )
})

const hasOpenAIUsageFallback = computed(() => {
  if (props.account.platform !== 'openai' || props.account.type !== 'oauth') return false
  return !!usageInfo.value?.five_hour || !!usageInfo.value?.seven_day
})

const codexTurnTickets = computed(() => props.account.codex_turn_tickets ?? [])

function shortCodexTicketModel(model: string) {
  if (model === 'gpt-6-astra') return 'astra'
  if (model === 'gpt-5.6-sol') return 'sol'
  return model
}

function formatCodexTicketRemaining(seconds: number) {
  const total = Math.max(0, Math.floor(seconds || 0))
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${m}m${String(s).padStart(2, '0')}s`
}

const openAISevenDayEstimatedTotalCost = computed(() => {
  const sevenDay = usageInfo.value?.seven_day
  const utilization = sevenDay?.utilization
  const currentCost = sevenDay?.window_stats?.cost
  if (
    typeof utilization !== 'number' ||
    typeof currentCost !== 'number' ||
    !Number.isFinite(utilization) ||
    !Number.isFinite(currentCost) ||
    utilization <= 0 ||
    currentCost <= 0
  ) {
    return null
  }

  const estimate = (currentCost * 100) / utilization
  return Number.isFinite(estimate) && estimate > 0 ? estimate : null
})

const openAIUsageRefreshKey = computed(() => buildOpenAIUsageRefreshKey(props.account))

const shouldAutoLoadUsageOnMount = computed(() => {
  return shouldFetchUsage.value
})

const shouldLazyLoadOnMobile = computed(() => {
  return shouldFetchUsage.value && !isDesktopViewport.value
})

// Antigravity quota types (用于 API 返回的数据)
interface AntigravityUsageResult {
  utilization: number
  resetTime: string | null
}

// ===== Antigravity quota from API (usageInfo.antigravity_quota) =====

// 检查是否有从 API 获取的配额数据
const hasAntigravityQuotaFromAPI = computed(() => {
  return usageInfo.value?.antigravity_quota && Object.keys(usageInfo.value.antigravity_quota).length > 0
})

// 从 API 配额数据中获取使用率（多模型取最高使用率）
const getAntigravityUsageFromAPI = (
  modelNames: string[]
): AntigravityUsageResult | null => {
  const quota = usageInfo.value?.antigravity_quota
  if (!quota) return null

  let maxUtilization = 0
  let earliestReset: string | null = null

  for (const model of modelNames) {
    const modelQuota = quota[model]
    if (!modelQuota) continue

    if (modelQuota.utilization > maxUtilization) {
      maxUtilization = modelQuota.utilization
    }
    if (modelQuota.reset_time) {
      if (!earliestReset || modelQuota.reset_time < earliestReset) {
        earliestReset = modelQuota.reset_time
      }
    }
  }

  // 如果没有找到任何匹配的模型
  if (maxUtilization === 0 && earliestReset === null) {
    const hasAnyData = modelNames.some((m) => quota[m])
    if (!hasAnyData) return null
  }

  return {
    utilization: maxUtilization,
    resetTime: earliestReset
  }
}

// Gemini 3 Pro from API
const antigravity3ProUsageFromAPI = computed(() =>
  getAntigravityUsageFromAPI(['gemini-3-pro-low', 'gemini-3-pro-high', 'gemini-3-pro-preview'])
)

// Gemini 3 Flash from API
const antigravity3FlashUsageFromAPI = computed(() => getAntigravityUsageFromAPI(['gemini-3-flash']))

// Gemini Image from API
const antigravity3ImageUsageFromAPI = computed(() =>
  getAntigravityUsageFromAPI(['gemini-2.5-flash-image', 'gemini-3.1-flash-image', 'gemini-3-pro-image'])
)

// Claude from API (all Claude model variants)
const antigravityClaudeUsageFromAPI = computed(() =>
  getAntigravityUsageFromAPI([
    'claude-fable-5-1',
    'claude-fable-5',
    'claude-sonnet-4-5', 'claude-opus-4-5-thinking',
    'claude-sonnet-4-6', 'claude-opus-4-6', 'claude-opus-4-6-thinking',
    'claude-opus-4-7', 'claude-opus-4-8',
  ])
)

const aiCreditsDisplay = computed(() => {
  const credits = usageInfo.value?.ai_credits
  if (!credits || credits.length === 0) return null
  const total = credits.reduce((sum, credit) => sum + (credit.amount ?? 0), 0)
  if (total <= 0) return null
  return total.toFixed(0)
})

// Antigravity 账户类型（从 load_code_assist 响应中提取）
const antigravityTier = computed(() => {
  const extra = props.account.extra as Record<string, unknown> | undefined
  if (!extra) return null

  const loadCodeAssist = extra.load_code_assist as Record<string, unknown> | undefined
  if (!loadCodeAssist) return null

  // 优先取 paidTier，否则取 currentTier
  const paidTier = loadCodeAssist.paidTier as Record<string, unknown> | undefined
  if (paidTier && typeof paidTier.id === 'string') {
    return paidTier.id
  }

  const currentTier = loadCodeAssist.currentTier as Record<string, unknown> | undefined
  if (currentTier && typeof currentTier.id === 'string') {
    return currentTier.id
  }

  return null
})

// Gemini 账户类型（从 credentials 中提取）
const geminiTier = computed(() => {
  if (props.account.platform !== 'gemini') return null
  const creds = props.account.credentials as GeminiCredentials | undefined
  return creds?.tier_id || null
})

const geminiOAuthType = computed(() => {
  if (props.account.platform !== 'gemini') return null
  const creds = props.account.credentials as GeminiCredentials | undefined
  return (creds?.oauth_type || '').trim() || null
})

// Gemini 是否为 Code Assist OAuth
const isGeminiCodeAssist = computed(() => {
  if (props.account.platform !== 'gemini') return false
  const creds = props.account.credentials as GeminiCredentials | undefined
  return creds?.oauth_type === 'code_assist' || (!creds?.oauth_type && !!creds?.project_id)
})

const geminiChannelShort = computed((): 'ai studio' | 'gcp' | 'google one' | 'client' | null => {
  if (props.account.platform !== 'gemini') return null
  if (isGeminiOpenAIProtocolAccount(props.account)) return null

  // API Key accounts are AI Studio.
  if (props.account.type === 'apikey') return 'ai studio'

  if (geminiOAuthType.value === 'google_one') return 'google one'
  if (isGeminiCodeAssist.value) return 'gcp'
  if (geminiOAuthType.value === 'ai_studio') return 'client'

  // Fallback (unknown legacy data): treat as AI Studio.
  return 'ai studio'
})

const geminiUserLevel = computed((): string | null => {
  if (props.account.platform !== 'gemini') return null
  if (isGeminiOpenAIProtocolAccount(props.account)) return null

  const tier = (geminiTier.value || '').toString().trim()
  const tierLower = tier.toLowerCase()
  const tierUpper = tier.toUpperCase()

  // Google One: free / pro / ultra
  if (geminiOAuthType.value === 'google_one') {
    if (tierLower === 'google_one_free') return 'free'
    if (tierLower === 'google_ai_pro') return 'pro'
    if (tierLower === 'google_ai_ultra') return 'ultra'

    // Backward compatibility (legacy tier markers)
    if (tierUpper === 'AI_PREMIUM' || tierUpper === 'GOOGLE_ONE_STANDARD') return 'pro'
    if (tierUpper === 'GOOGLE_ONE_UNLIMITED') return 'ultra'
    if (tierUpper === 'FREE' || tierUpper === 'GOOGLE_ONE_BASIC' || tierUpper === 'GOOGLE_ONE_UNKNOWN' || tierUpper === '') return 'free'

    return null
  }

  // GCP Code Assist: standard / enterprise
  if (isGeminiCodeAssist.value) {
    if (tierLower === 'gcp_enterprise') return 'enterprise'
    if (tierLower === 'gcp_standard') return 'standard'

    // Backward compatibility
    if (tierUpper.includes('ULTRA') || tierUpper.includes('ENTERPRISE')) return 'enterprise'
    return 'standard'
  }

  // AI Studio (API Key) and Client OAuth: free / paid
  if (props.account.type === 'apikey' || geminiOAuthType.value === 'ai_studio') {
    if (tierLower === 'aistudio_paid') return 'paid'
    if (tierLower === 'aistudio_free') return 'free'

    // Backward compatibility
    if (tierUpper.includes('PAID') || tierUpper.includes('PAYG') || tierUpper.includes('PAY')) return 'paid'
    if (tierUpper.includes('FREE')) return 'free'
    if (props.account.type === 'apikey') return 'free'
    return null
  }

  return null
})

// Gemini 认证类型（按要求：授权方式简称 + 用户等级）
const geminiAuthTypeLabel = computed(() => {
  if (props.account.platform !== 'gemini') return null
  if (!geminiChannelShort.value) return null
  return geminiUserLevel.value ? `${geminiChannelShort.value} ${geminiUserLevel.value}` : geminiChannelShort.value
})

// Gemini 账户类型徽章颜色（UBadge subtle）
const geminiTierColor = computed<BadgeColor>(() => {
  // Use channel+level to choose a stable color without depending on raw tier_id variants.
  const channel = geminiChannelShort.value
  const level = geminiUserLevel.value

  if (channel === 'client' || channel === 'ai studio') return 'info'

  if (channel === 'google one') {
    if (level === 'ultra') return 'primary'
    if (level === 'pro') return 'info'
    return 'neutral'
  }

  if (channel === 'gcp') {
    if (level === 'enterprise') return 'primary'
    return 'info'
  }

  return 'neutral'
})

// Gemini 配额政策信息
const geminiQuotaPolicyChannel = computed(() => {
  if (geminiOAuthType.value === 'google_one') {
    return t('admin.accounts.gemini.quotaPolicy.rows.googleOne.channel')
  }
  if (isGeminiCodeAssist.value) {
    return t('admin.accounts.gemini.quotaPolicy.rows.gcp.channel')
  }
  return t('admin.accounts.gemini.quotaPolicy.rows.aiStudio.channel')
})

const geminiQuotaPolicyLimits = computed(() => {
  const tierLower = (geminiTier.value || '').toString().trim().toLowerCase()

  if (geminiOAuthType.value === 'google_one') {
    if (tierLower === 'google_ai_ultra' || geminiUserLevel.value === 'ultra') {
      return t('admin.accounts.gemini.quotaPolicy.rows.googleOne.limitsUltra')
    }
    if (tierLower === 'google_ai_pro' || geminiUserLevel.value === 'pro') {
      return t('admin.accounts.gemini.quotaPolicy.rows.googleOne.limitsPro')
    }
    return t('admin.accounts.gemini.quotaPolicy.rows.googleOne.limitsFree')
  }

  if (isGeminiCodeAssist.value) {
    if (tierLower === 'gcp_enterprise' || geminiUserLevel.value === 'enterprise') {
      return t('admin.accounts.gemini.quotaPolicy.rows.gcp.limitsEnterprise')
    }
    return t('admin.accounts.gemini.quotaPolicy.rows.gcp.limitsStandard')
  }

  // AI Studio (API Key / custom OAuth)
  if (tierLower === 'aistudio_paid' || geminiUserLevel.value === 'paid') {
    return t('admin.accounts.gemini.quotaPolicy.rows.aiStudio.limitsPaid')
  }
  return t('admin.accounts.gemini.quotaPolicy.rows.aiStudio.limitsFree')
})

const geminiQuotaPolicyDocsUrl = computed(() => {
  if (geminiOAuthType.value === 'google_one' || isGeminiCodeAssist.value) {
    return 'https://developers.google.com/gemini-code-assist/resources/quotas'
  }
  return 'https://ai.google.dev/pricing'
})

const geminiUsesSharedDaily = computed(() => {
  if (props.account.platform !== 'gemini') return false
  // Per requirement: Google One & GCP are shared RPD pools (no per-model breakdown).
  return (
    !!usageInfo.value?.gemini_shared_daily ||
    !!usageInfo.value?.gemini_shared_minute ||
    geminiOAuthType.value === 'google_one' ||
    isGeminiCodeAssist.value
  )
})

const geminiUsageBars = computed(() => {
  if (props.account.platform !== 'gemini') return []
  if (!usageInfo.value) return []

  const bars: Array<{
    key: string
    label: string
    utilization: number
    resetsAt: string | null
    windowStats?: WindowStats | null
    color: 'indigo' | 'emerald'
  }> = []

  if (geminiUsesSharedDaily.value) {
    const sharedDaily = usageInfo.value.gemini_shared_daily
    if (sharedDaily) {
      bars.push({
        key: 'shared_daily',
        label: '1d',
        utilization: sharedDaily.utilization,
        resetsAt: sharedDaily.resets_at,
        windowStats: sharedDaily.window_stats,
        color: 'indigo'
      })
    }
    return bars
  }

  const pro = usageInfo.value.gemini_pro_daily
  if (pro) {
    bars.push({
      key: 'pro_daily',
      label: 'pro',
      utilization: pro.utilization,
      resetsAt: pro.resets_at,
      windowStats: pro.window_stats,
      color: 'indigo'
      })
  }

  const flash = usageInfo.value.gemini_flash_daily
  if (flash) {
    bars.push({
      key: 'flash_daily',
      label: 'flash',
      utilization: flash.utilization,
      resetsAt: flash.resets_at,
      windowStats: flash.window_stats,
      color: 'emerald'
    })
  }

  return bars
})

interface GrokQuotaBarInfo {
  utilization: number
  resetsAt: string | null
  windowStats?: WindowStats | null
}

const grokBilling = computed(() => usageInfo.value?.grok_billing || null)
const grokLocalUsage7d = computed(() => (
  usageInfo.value?.grok_local_usage_7d || usageInfo.value?.seven_day?.window_stats || null
))
const grokLocalUsageMonthly = computed(() => (
  usageInfo.value?.grok_local_usage_monthly || usageInfo.value?.thirty_day?.window_stats || null
))
const grokWeeklyBillingBar = computed((): GrokQuotaBarInfo | null => {
  const billing = grokBilling.value
  if (billing?.period_type?.toLowerCase() !== 'weekly' || billing.usage_percent == null) {
    return null
  }
  return {
    utilization: Math.min(100, Math.max(0, billing.usage_percent)),
    resetsAt: billing.period_end || null,
    windowStats: grokLocalUsage7d.value
  }
})
// Monthly used/limit % from billing probe (used_percent or derived from cents).
const grokMonthlyBillingBar = computed((): GrokQuotaBarInfo | null => {
  const billing = grokBilling.value
  if (!billing) return null
  let utilization: number | null = null
  if (billing.used_percent != null && Number.isFinite(billing.used_percent)) {
    utilization = billing.used_percent
  } else if (
    billing.monthly_limit_cents != null &&
    billing.monthly_limit_cents > 0 &&
    billing.used_cents != null
  ) {
    utilization = (billing.used_cents / billing.monthly_limit_cents) * 100
  }
  if (utilization == null) return null
  // Avoid duplicating the weekly bar when period_type is weekly-only without monthly.
  if (billing.period_type?.toLowerCase() === 'weekly' && billing.monthly_limit_cents == null) {
    return null
  }
  return {
    utilization: Math.min(100, Math.max(0, utilization)),
    resetsAt: billing.billing_period_end || billing.period_end || null,
    windowStats: grokLocalUsageMonthly.value
  }
})
const formatGrokMoney = (value?: number | null) => {
  if (value == null || Number.isNaN(value)) return '0'
  if (value >= 1000) return formatCompactNumber(value)
  if (value >= 100) return value.toFixed(0)
  if (value >= 10) return value.toFixed(1)
  return value.toFixed(2)
}
// Prepaid chip only when there is a positive prepaid balance.
// Used/limit only when monthly limit is a positive number (0 means unlimited / unset).
const grokPrepaidMoneyLine = computed(() => {
  const billing = grokBilling.value
  if (!billing) return null
  const prepaid = billing.prepaid_balance
  const showPrepaid = prepaid != null && Number.isFinite(prepaid) && prepaid > 0
  const limitRaw =
    billing.monthly_limit != null
      ? billing.monthly_limit
      : billing.monthly_limit_cents != null
        ? billing.monthly_limit_cents / 100
        : null
  const showUsedLimit = limitRaw != null && Number.isFinite(limitRaw) && limitRaw > 0
  if (!showPrepaid && !showUsedLimit) return null
  const used =
    billing.monthly_used != null
      ? billing.monthly_used
      : billing.used_cents != null
        ? billing.used_cents / 100
        : 0
  return {
    showPrepaid,
    showUsedLimit,
    prepaid: showPrepaid ? formatGrokMoney(prepaid) : null,
    used: showUsedLimit ? formatGrokMoney(used) : null,
    limit: showUsedLimit ? formatGrokMoney(limitRaw) : null
  }
})
const grokPlanLabelIsFree = (value: string) => value.includes('free') || value.includes('basic')
const grokPlanLabelIsPaid = (value: string) => {
  return value !== '' && !grokPlanLabelIsFree(value) && !value.includes('unknown')
}
const grokIsFree = computed(() => {
  if (props.account.platform !== 'grok' || props.account.type !== 'oauth') return false
  const billing = grokBilling.value
  const plan = (billing?.plan || '').trim().toLowerCase()
  const tier = (usageInfo.value?.subscription_tier || '').trim().toLowerCase()
  const entitlement = (usageInfo.value?.grok_entitlement_status || '').toLowerCase()
  if (grokPlanLabelIsFree(tier)) return true
  if (grokPlanLabelIsPaid(tier)) return false
  if (
    billing?.usage_percent != null ||
    billing?.used_percent != null ||
    (billing?.monthly_limit_cents != null && billing.monthly_limit_cents > 0)
  ) return false
  if (grokPlanLabelIsPaid(plan)) return false
  if (
    grokPlanLabelIsFree(plan) ||
    grokPlanLabelIsFree(entitlement)
  ) return true
  return billing != null
})
const grokFreeQuotaUsage = computed(() => usageInfo.value?.grok_local_usage_24h || null)
const grokFreeTokenBar = computed(() => {
  if (!grokIsFree.value || !grokFreeQuotaUsage.value) return null
  const limit = usageInfo.value?.grok_free_token_limit
  if (typeof limit !== 'number' || limit <= 0) return null
  const used = Math.max(0, grokFreeQuotaUsage.value.tokens || 0)
  return { utilization: Math.min(100, (used / limit) * 100), limit }
})
const grokQuotaUnknown = computed(() => {
  if (props.account.platform !== 'grok') return false
  if (grokIsFree.value) {
    return !grokFreeTokenBar.value
  }
  if (grokWeeklyBillingBar.value || grokMonthlyBillingBar.value || grokPrepaidMoneyLine.value) {
    return false
  }
  return usageInfo.value?.grok_quota_snapshot_state !== 'observed'
})
const grokQuotaUnknownLabel = computed(() => {
  return usageInfo.value?.grok_quota_snapshot_state === 'no_headers'
    ? t('admin.accounts.usageWindow.grokNoHeaders')
    : t('admin.accounts.usageWindow.grokUnknown')
})
const grokRetryAfterLabel = computed(() => {
  const seconds = usageInfo.value?.grok_retry_after_seconds
  if (seconds == null || seconds <= 0) return null
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.ceil(seconds / 60)
  return `${minutes}m`
})

// 账户类型显示标签
const antigravityTierLabel = computed(() => {
  switch (antigravityTier.value) {
    case 'free-tier':
      return t('admin.accounts.tier.free')
    case 'g1-pro-tier':
      return t('admin.accounts.tier.pro')
    case 'g1-ultra-tier':
      return t('admin.accounts.tier.ultra')
    default:
      return null
  }
})

// 账户类型徽章颜色（UBadge subtle）
const antigravityTierColor = computed<BadgeColor>(() => {
  switch (antigravityTier.value) {
    case 'g1-pro-tier':
      return 'info'
    case 'g1-ultra-tier':
      return 'primary'
    default:
      return 'neutral'
  }
})

// 检测账户是否有不合格状态（ineligibleTiers）
const hasIneligibleTiers = computed(() => {
  const extra = props.account.extra as Record<string, unknown> | undefined
  if (!extra) return false

  const loadCodeAssist = extra.load_code_assist as Record<string, unknown> | undefined
  if (!loadCodeAssist) return false

  const ineligibleTiers = loadCodeAssist.ineligibleTiers as unknown[] | undefined
  return Array.isArray(ineligibleTiers) && ineligibleTiers.length > 0
})

// Antigravity 403 forbidden 状态
const isForbidden = computed(() => !!usageInfo.value?.is_forbidden)
const forbiddenType = computed(() => usageInfo.value?.forbidden_type || 'forbidden')
const validationURL = computed(() => usageInfo.value?.validation_url || '')

// 需要重新授权（401）
const needsReauth = computed(() => !!usageInfo.value?.needs_reauth)

// 降级错误标签（rate_limited / network_error）
const usageErrorLabel = computed(() => {
  const code = usageInfo.value?.error_code
  if (code === 'rate_limited') return t('admin.accounts.rateLimited')
  return t('admin.accounts.usageError')
})

const forbiddenLabel = computed(() => {
  switch (forbiddenType.value) {
    case 'validation':
      return t('admin.accounts.forbiddenValidation')
    case 'violation':
      return t('admin.accounts.forbiddenViolation')
    default:
      return t('admin.accounts.forbidden')
  }
})

const forbiddenBadgeColor = computed<BadgeColor>(() => (forbiddenType.value === 'validation' ? 'warning' : 'error'))

const linkCopied = ref(false)
const copyValidationURL = async () => {
  if (!validationURL.value) return
  try {
    await navigator.clipboard.writeText(validationURL.value)
    linkCopied.value = true
    setTimeout(() => { linkCopied.value = false }, 2000)
  } catch {
    // fallback: ignore
  }
}

const isAnthropicOAuthOrSetupToken = computed(() => {
  return props.account.platform === 'anthropic' && (props.account.type === 'oauth' || props.account.type === 'setup-token')
})

const requestParentBatchUsage = (options?: { force?: boolean }) => {
  if (!isBatchManaged.value || !shouldFetchUsage.value) return
  props.requestBatchedUsage?.(props.account, options)
}

const syncManagedUsageState = () => {
  if (!isBatchManaged.value) return
  usageInfo.value = props.batchedUsage ?? null
  error.value = props.batchedUsageError ?? null
  loading.value = props.batchedUsageLoading === true
}

const loadUsage = async (options?: { source?: 'passive' | 'active'; bypassCache?: boolean }) => {
  if (!shouldFetchUsage.value) return
  if (isBatchManaged.value) {
    requestParentBatchUsage({ force: options?.bypassCache === true })
    return
  }

  // Check cache
  if (!options?.bypassCache) {
    const cached = _usageCache.get(props.account.id)
    if (cached && Date.now() - cached.ts < USAGE_CACHE_TTL) {
      usageInfo.value = cached.data
      loading.value = false
      return
    }
  }

  loading.value = true
  error.value = null

  try {
		const fetchFn = () => options?.source
			? adminAPI.accounts.getUsage(props.account.id, options.source, options.bypassCache === true)
			: adminAPI.accounts.getUsage(props.account.id)
    const result = await enqueueUsageRequest(props.account, fetchFn)
    if (!unmounted.value) {
      usageInfo.value = result
      _usageCache.set(props.account.id, { data: result, ts: Date.now() })
    }
  } catch (e: any) {
    if (!unmounted.value) {
      error.value = t('common.error')
      console.error('Failed to load usage:', e)
    }
  } finally {
    if (!unmounted.value) loading.value = false
  }
}

const flushPendingAutoLoad = () => {
  if (!pendingAutoLoad.value) return
  const source = pendingAutoLoadSource.value
  pendingAutoLoad.value = false
  pendingAutoLoadSource.value = undefined
  loadUsage({ source }).catch((e) => {
    console.error('Failed to load deferred usage:', e)
  })
}

const requestAutoLoad = (source?: 'passive' | 'active') => {
  if (!shouldFetchUsage.value) return
  if (shouldLazyLoadOnMobile.value && !hasEnteredViewport.value) {
    pendingAutoLoad.value = true
    pendingAutoLoadSource.value = source
    return
  }
  loadUsage({ source }).catch((e) => {
    console.error('Failed to auto load usage:', e)
  })
}

const detachVisibilityObserver = () => {
  visibilityObserver?.disconnect()
  visibilityObserver = null
}

const attachVisibilityObserver = () => {
  detachVisibilityObserver()
  if (!shouldLazyLoadOnMobile.value || hasEnteredViewport.value) return
  if (typeof window === 'undefined' || typeof IntersectionObserver === 'undefined') {
    hasEnteredViewport.value = true
    flushPendingAutoLoad()
    return
  }
  if (!rootRef.value) return

  visibilityObserver = new IntersectionObserver((entries) => {
    if (!entries.some((entry) => entry.isIntersecting)) return
    hasEnteredViewport.value = true
    detachVisibilityObserver()
    flushPendingAutoLoad()
  }, {
    root: null,
    rootMargin: '200px 0px',
    threshold: 0.01
  })
  visibilityObserver.observe(rootRef.value)
}

const loadActiveUsage = async () => {
  activeQueryLoading.value = true
  try {
    usageInfo.value = await adminAPI.accounts.getUsage(props.account.id, 'active', true)
  } catch (e: any) {
    console.error('Failed to load active usage:', e)
  } finally {
    activeQueryLoading.value = false
  }
}

// The probe persists upstream quota state; refresh this cell so its compact
// bars and entitlement status reflect the newly observed snapshot.
const handleGrokProbed = async () => {
  await loadUsage({ source: 'active', bypassCache: true })
}

// ===== API Key quota progress bars =====

interface QuotaBarInfo {
  utilization: number
  resetsAt: string | null
}

const makeQuotaBar = (
  used: number,
  limit: number,
  startKey?: string
): QuotaBarInfo => {
  const utilization = limit > 0 ? (used / limit) * 100 : 0
  let resetsAt: string | null = null
  if (startKey) {
    const extra = props.account.extra as Record<string, unknown> | undefined
    const isDaily = startKey.includes('daily')
    const mode = isDaily
      ? (extra?.quota_daily_reset_mode as string) || 'rolling'
      : (extra?.quota_weekly_reset_mode as string) || 'rolling'

    if (mode === 'fixed') {
      // Use pre-computed next reset time for fixed mode
      const resetAtKey = isDaily ? 'quota_daily_reset_at' : 'quota_weekly_reset_at'
      resetsAt = (extra?.[resetAtKey] as string) || null
    } else {
      // Rolling mode: compute from start + period
      const startStr = extra?.[startKey] as string | undefined
      if (startStr) {
        const startDate = new Date(startStr)
        const periodMs = isDaily ? 24 * 60 * 60 * 1000 : 7 * 24 * 60 * 60 * 1000
        resetsAt = new Date(startDate.getTime() + periodMs).toISOString()
      }
    }
  }
  return { utilization, resetsAt }
}

const hasApiKeyQuota = computed(() => {
  if (props.account.type !== 'apikey' && props.account.type !== 'bedrock') return false
  return (
    (props.account.quota_daily_limit ?? 0) > 0 ||
    (props.account.quota_weekly_limit ?? 0) > 0 ||
    (props.account.quota_limit ?? 0) > 0
  )
})

const quotaDailyBar = computed((): QuotaBarInfo | null => {
  const limit = props.account.quota_daily_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.account.quota_daily_used ?? 0, limit, 'quota_daily_start')
})

const quotaWeeklyBar = computed((): QuotaBarInfo | null => {
  const limit = props.account.quota_weekly_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.account.quota_weekly_used ?? 0, limit, 'quota_weekly_start')
})

const quotaTotalBar = computed((): QuotaBarInfo | null => {
  const limit = props.account.quota_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.account.quota_used ?? 0, limit)
})

const handleQuotaResetAccountUpdated = (account: Account) => {
  emit('account-updated', account)
}

const handleOllamaCloudUsageUpdated = (state: NonNullable<Account['ollama_cloud_usage']>) => {
  emit('account-updated', { ...props.account, ollama_cloud_usage: state })
}

// ===== Key account today stats formatters =====

const formatKeyRequests = computed(() => {
  if (!props.todayStats) return ''
  return formatCompactNumber(props.todayStats.requests, { allowBillions: false })
})

const formatKeyTokens = computed(() => {
  if (!props.todayStats) return ''
  return formatCompactNumber(props.todayStats.tokens)
})

const formatKeyCost = computed(() => {
  if (!props.todayStats) return '0.00'
  return props.todayStats.cost.toFixed(2)
})

const formatKeyUserCost = computed(() => {
  if (!props.todayStats || props.todayStats.user_cost == null) return '0.00'
  return props.todayStats.user_cost.toFixed(2)
})

onMounted(() => {
  if (typeof window !== 'undefined') {
    desktopViewportMediaQuery = window.matchMedia(desktopViewportQuery)
    isDesktopViewport.value = desktopViewportMediaQuery.matches
    desktopViewportListener = (event: MediaQueryListEvent) => {
      isDesktopViewport.value = event.matches
    }
    if (typeof desktopViewportMediaQuery.addEventListener === 'function') {
      desktopViewportMediaQuery.addEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.addListener(desktopViewportListener)
    }
  }

  if (isBatchManaged.value) {
    syncManagedUsageState()
    requestParentBatchUsage()
    return
  }

  if (!shouldAutoLoadUsageOnMount.value) return
  const source = isAnthropicOAuthOrSetupToken.value ? 'passive' : undefined
  requestAutoLoad(source)
})

watch(
  () => [props.batchedUsage, props.batchedUsageError, props.batchedUsageLoading, isBatchManaged.value] as const,
  () => {
    syncManagedUsageState()
  },
  { immediate: true, deep: true }
)

watch(isBatchManaged, (managed, wasManaged) => {
  if (managed && !wasManaged) {
    syncManagedUsageState()
    requestParentBatchUsage()
  }
})

watch(
  () => [props.account.id, props.account.platform, props.account.type, isBatchManaged.value] as const,
  ([accountID, platform, accountType, managed], [previousAccountID, previousPlatform, previousAccountType]) => {
    if (
      accountID === previousAccountID &&
      platform === previousPlatform &&
      accountType === previousAccountType
    ) {
      return
    }
    if (!managed || !shouldFetchUsage.value) return
    syncManagedUsageState()
    requestParentBatchUsage()
  },
  { flush: 'post' }
)

watch(openAIUsageRefreshKey, (nextKey, prevKey) => {
  if (!prevKey || nextKey === prevKey) return
  if (props.account.platform !== 'openai' || props.account.type !== 'oauth') return

  if (isBatchManaged.value) {
    requestParentBatchUsage({ force: true })
    return
  }

  _usageCache.delete(props.account.id)
  requestAutoLoad()
})

watch(
  () => props.manualRefreshToken,
  (nextToken, prevToken) => {
    if (nextToken === prevToken) return
    if (!shouldFetchUsage.value) return

    if (isBatchManaged.value) {
      requestParentBatchUsage({ force: true })
      return
    }

    const source = isAnthropicOAuthOrSetupToken.value ? 'passive' : undefined
    _usageCache.delete(props.account.id)
    loadUsage({ source, bypassCache: true }).catch((e) => {
      console.error('Failed to refresh usage after manual refresh:', e)
    })
  }
)

watch(
  [rootRef, shouldLazyLoadOnMobile],
  () => {
    if (shouldLazyLoadOnMobile.value) {
      attachVisibilityObserver()
      return
    }
    detachVisibilityObserver()
  },
  { immediate: true, flush: 'post' }
)

watch(isDesktopViewport, (isDesktop) => {
  if (isDesktop) {
    detachVisibilityObserver()
    hasEnteredViewport.value = true
    flushPendingAutoLoad()
    return
  }
  hasEnteredViewport.value = false
  attachVisibilityObserver()
})

onUnmounted(() => {
  detachVisibilityObserver()
  if (desktopViewportMediaQuery && desktopViewportListener) {
    if (typeof desktopViewportMediaQuery.removeEventListener === 'function') {
      desktopViewportMediaQuery.removeEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.removeListener(desktopViewportListener)
    }
  }
  desktopViewportListener = null
  desktopViewportMediaQuery = null
})
</script>
