<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <AdminPageHeader
          :title="t('admin.channels.title')"
          :description="t('admin.channels.description')"
        >
          <template #actions>
            <UButton
              icon="i-lucide-plus"
              :label="t('admin.channels.createChannel', 'Create Channel')"
              @click="openCreateDialog"
            />
          </template>
        </AdminPageHeader>
      </template>
      <template #filters>
        <PageToolbar>
          <UInput
            v-model="searchQuery"
            icon="i-lucide-search"
            type="text"
            :placeholder="t('admin.channels.searchChannels', 'Search channels...')"
            class="w-96 max-w-full sm:w-72"
            @update:model-value="handleSearch"
          />
          <template #right>
            <Select
              v-model="filters.status"
              :options="statusFilterOptions"
              :placeholder="t('admin.channels.allStatus', 'All Status')"
              class="w-40"
              @change="loadChannels"
            />
            <UButton
              icon="i-lucide-refresh-cw"
              color="neutral"
              variant="outline"
              square
              :disabled="loading"
              :ui="{ leadingIcon: loading ? 'animate-spin' : '' }"
              :title="t('common.refresh', 'Refresh')"
              :aria-label="t('common.refresh', 'Refresh')"
              @click="loadChannels"
            />
            <UDropdownMenu :items="columnMenuItems" :content="{ align: 'end' }">
              <UButton
                :label="t('admin.channels.displayColumns')"
                color="neutral"
                variant="outline"
                trailing-icon="i-lucide-settings-2"
              />
            </UDropdownMenu>
          </template>
        </PageToolbar>
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="channels"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #cell-name="{ value }">
            <span class="font-medium text-highlighted">{{ value }}</span>
          </template>

          <template #cell-description="{ value }">
            <span class="text-sm text-muted">{{ value || '-' }}</span>
          </template>

          <template #cell-status="{ row }">
            <USwitch
              :model-value="row.status === 'active'"
              :aria-label="t('admin.channels.columns.status', 'Status')"
              @update:model-value="toggleChannelStatus(row)"
            />
          </template>

          <template #cell-group_count="{ row }">
            <UBadge color="neutral" variant="subtle">
              {{ (row.group_ids || []).length }}
              {{ t('admin.channels.groupsUnit', 'groups') }}
            </UBadge>
          </template>

          <template #cell-pricing_count="{ row }">
            <UBadge color="neutral" variant="subtle">
              {{ (row.model_pricing || []).length }}
              {{ t('admin.channels.pricingUnit', 'pricing rules') }}
            </UBadge>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm text-muted">
              {{ formatDate(value) }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-0.5">
              <AdminRowAction :label="t('common.edit', 'Edit')" icon="edit" tone="primary" @click="openEditDialog(row)" />
              <AdminRowAction :label="t('common.delete', 'Delete')" icon="trash" tone="danger" @click="handleDelete(row)" />
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.channels.noChannelsYet', 'No Channels Yet')"
              :description="t('admin.channels.createFirstChannel', 'Create your first channel to manage model pricing')"
              :action-text="t('admin.channels.createChannel', 'Create Channel')"
              @action="openCreateDialog"
            />
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <!-- Create/Edit Dialog -->
    <BaseDialog
      :show="showDialog"
      :title="editingChannel ? t('admin.channels.editChannel', 'Edit Channel') : t('admin.channels.createChannel', 'Create Channel')"
      width="extra-wide"
      @close="closeDialog"
    >
      <div class="channel-dialog-body">
        <!-- Tab bar: basic settings + one tab per enabled platform -->
        <UTabs
          :items="dialogTabs"
          :model-value="activeTab"
          :content="false"
          variant="link"
          size="sm"
          class="shrink-0"
          :ui="{ list: 'overflow-x-auto' }"
          @update:model-value="activeTab = String($event)"
        >
          <template #leading="{ item }">
            <PlatformIcon v-if="item.platform" :platform="item.platform" size="xs" />
          </template>
        </UTabs>

        <!-- Tab Content -->
        <UForm id="channel-form" class="flex-1 overflow-y-auto pt-5" @submit="handleSubmit">
          <!-- Basic Settings Tab -->
          <div v-show="activeTab === 'basic'" class="space-y-5">
            <UFormField :label="t('admin.channels.form.name', 'Name')" required>
              <UInput
                v-model="form.name"
                type="text"
                required
                class="w-full"
                :placeholder="t('admin.channels.form.namePlaceholder', 'Enter channel name')"
              />
            </UFormField>

            <UFormField :label="t('admin.channels.form.description', 'Description')">
              <UTextarea
                v-model="form.description"
                :rows="2"
                autoresize
                class="w-full"
                :placeholder="t('admin.channels.form.descriptionPlaceholder', 'Optional description')"
              />
            </UFormField>

            <UFormField v-if="editingChannel" :label="t('admin.channels.form.status', 'Status')">
              <Select v-model="form.status" :options="statusEditOptions" />
            </UFormField>

            <UCheckbox
              v-model="form.restrict_models"
              :label="t('admin.channels.form.restrictModels', 'Restrict Models')"
              :description="t('admin.channels.form.restrictModelsHint', 'When enabled, only models in the pricing list are allowed. Others will be rejected.')"
              :ui="{ description: 'text-xs' }"
            />

            <UFormField
              :label="t('admin.channels.form.billingModelSource', 'Billing Basis')"
              :description="t('admin.channels.form.billingModelSourceHint', 'Controls which model name is used for pricing lookup')"
              :ui="{ description: 'text-xs' }"
            >
              <Select v-model="form.billing_model_source" :options="billingModelSourceOptions" />
            </UFormField>

            <UFormField :label="t('admin.channels.form.platformConfig')">
              <div class="flex flex-wrap gap-x-5 gap-y-2.5 pt-1">
                <UCheckbox
                  v-for="p in platformOrder"
                  :key="p"
                  :model-value="activePlatforms.includes(p)"
                  @update:model-value="togglePlatform(p)"
                >
                  <template #label>
                    <span class="inline-flex items-center gap-1.5">
                      <PlatformIcon :platform="p" size="xs" />
                      {{ t('admin.groups.platforms.' + p, p) }}
                    </span>
                  </template>
                </UCheckbox>
              </div>
            </UFormField>

            <USeparator />

            <!-- Apply Pricing to Account Stats (toggle only in basic settings) -->
            <UFormField
              :label="t('admin.channels.form.applyPricingToAccountStats')"
              :description="t('admin.channels.form.applyPricingToAccountStatsDesc')"
              orientation="horizontal"
              :ui="toggleFieldUi"
            >
              <USwitch v-model="form.apply_pricing_to_account_stats" />
            </UFormField>
          </div>

          <!-- Platform Tab Content -->
          <div
            v-for="(section, sIdx) in form.platforms"
            :key="'tab-' + section.platform"
            v-show="section.enabled && activeTab === section.platform"
            class="space-y-4"
          >
            <!-- Groups -->
            <UFormField
              :label="t('admin.channels.form.groups', 'Associated Groups')"
              :hint="section.group_ids.length > 0 ? t('admin.channels.form.selectedCount', { count: section.group_ids.length }) : undefined"
              required
            >
              <div class="max-h-40 overflow-auto rounded-md bg-elevated/50 p-2.5 ring ring-inset ring-default">
                <p v-if="groupsLoading" class="py-1 text-center text-xs text-muted">
                  {{ t('common.loading', 'Loading...') }}
                </p>
                <p v-else-if="getGroupsForPlatform(section.platform).length === 0" class="py-1 text-center text-xs text-muted">
                  {{ t('admin.channels.form.noGroupsAvailable', 'No groups available') }}
                </p>
                <div v-else class="flex flex-wrap gap-x-4 gap-y-2">
                  <UCheckbox
                    v-for="group in getGroupsForPlatform(section.platform)"
                    :key="group.id"
                    size="sm"
                    :model-value="section.group_ids.includes(group.id)"
                    :disabled="isGroupInOtherChannel(group.id, section.platform)"
                    @update:model-value="toggleGroupInSection(sIdx, group.id)"
                  >
                    <template #label>
                      <span class="inline-flex items-center gap-1.5">
                        <span class="font-medium">{{ group.name }}</span>
                        <UBadge :label="`${group.rate_multiplier}x`" color="neutral" variant="subtle" size="sm" />
                        <span class="text-xs text-dimmed">{{ group.account_count || 0 }}</span>
                        <span
                          v-if="isGroupInOtherChannel(group.id, section.platform)"
                          class="text-xs text-dimmed"
                        >{{ getGroupInOtherChannelLabel(group.id) }}</span>
                      </span>
                    </template>
                  </UCheckbox>
                </div>
              </div>
            </UFormField>

            <!-- Web Search Emulation (Anthropic only, hidden when global disabled) -->
            <UFormField
              v-if="section.platform === 'anthropic' && webSearchGlobalEnabled"
              :label="t('admin.channels.form.webSearchEmulation')"
              :description="t('admin.channels.form.webSearchEmulationHint')"
              orientation="horizontal"
              :ui="{ ...toggleFieldUi, description: 'text-xs text-error' }"
            >
              <USwitch v-model="section.web_search_emulation" />
            </UFormField>

            <!-- Codex Image Generation Bridge (OpenAI only) -->
            <UFormField
              v-if="section.platform === 'openai'"
              :label="t('admin.channels.form.codexImageGenerationBridge')"
              :description="t('admin.channels.form.codexImageGenerationBridgeHint')"
              orientation="horizontal"
              :ui="{ ...toggleFieldUi, description: 'text-xs text-warning' }"
            >
              <USwitch v-model="section.codex_image_generation_bridge" />
            </UFormField>

            <!-- Bedrock CC Compatibility (Anthropic only) -->
            <UFormField
              v-if="section.platform === 'anthropic'"
              :label="t('admin.channels.form.bedrockCCCompat')"
              :description="t('admin.channels.form.bedrockCCCompatHint')"
              orientation="horizontal"
              :ui="{ ...toggleFieldUi, description: 'text-xs text-warning' }"
            >
              <USwitch v-model="section.bedrock_cc_compat" />
            </UFormField>

            <USeparator />

            <!-- Model Mapping -->
            <section class="space-y-2">
              <div class="flex items-center justify-between">
                <h4 class="text-sm font-medium text-highlighted">{{ t('admin.channels.form.modelMapping', 'Model Mapping') }}</h4>
                <UButton icon="i-lucide-plus" color="neutral" variant="ghost" size="xs" :label="t('common.add', 'Add')" @click="addMappingEntry(sIdx)" />
              </div>
              <p v-if="Object.keys(section.model_mapping).length === 0" class="text-xs text-muted">
                {{ t('admin.channels.form.noMappingRules', 'No mapping rules. Click "Add" to create one.') }}
              </p>
              <div v-else class="space-y-2">
                <div
                  v-for="(_, srcModel) in section.model_mapping"
                  :key="srcModel"
                  class="flex items-center gap-2"
                >
                  <UInput
                    :model-value="srcModel"
                    type="text"
                    size="sm"
                    class="flex-1"
                    :placeholder="t('admin.channels.form.mappingSource', 'Source model')"
                    @change="renameMappingKey(sIdx, srcModel, ($event.target as HTMLInputElement).value)"
                  />
                  <UIcon name="i-lucide-arrow-right" class="size-4 shrink-0 text-dimmed" />
                  <UInput
                    :model-value="section.model_mapping[srcModel]"
                    type="text"
                    size="sm"
                    class="flex-1"
                    :placeholder="t('admin.channels.form.mappingTarget', 'Target model')"
                    @input="section.model_mapping[srcModel] = ($event.target as HTMLInputElement).value"
                  />
                  <UButton
                    icon="i-lucide-trash-2"
                    color="neutral"
                    variant="ghost"
                    size="xs"
                    :aria-label="t('common.delete', 'Delete')"
                    :title="t('common.delete', 'Delete')"
                    @click="removeMappingEntry(sIdx, srcModel)"
                  />
                </div>
              </div>
            </section>

            <!-- Model Pricing -->
            <section class="space-y-2">
              <div class="flex items-center justify-between gap-2">
                <h4 class="text-sm font-medium text-highlighted">{{ t('admin.channels.form.modelPricing', 'Model Pricing') }}</h4>
                <div class="flex items-center gap-1">
                  <UButton
                    icon="i-lucide-refresh-cw"
                    color="neutral"
                    variant="ghost"
                    size="xs"
                    :loading="syncingPlatform === section.platform"
                    :disabled="syncingPlatform === section.platform"
                    :label="syncingPlatform === section.platform ? t('admin.channels.form.syncingModels') : t('admin.channels.form.syncLatestModels')"
                    @click="syncLatestModels(sIdx)"
                  />
                  <UButton icon="i-lucide-plus" color="neutral" variant="ghost" size="xs" :label="t('common.add', 'Add')" @click="addPricingEntry(sIdx)" />
                </div>
              </div>
              <p v-if="section.model_pricing.length === 0" class="text-xs text-muted">
                {{ t('admin.channels.form.noPricingRules', 'No pricing rules yet. Click "Add" to create one.') }}
              </p>
              <div v-else class="space-y-2">
                <PricingEntryCard
                  v-for="(entry, idx) in section.model_pricing"
                  :key="idx"
                  :entry="entry"
                  :platform="section.platform"
                  enable-time-pricing
                  enable-tier-multipliers
                  @update="updatePricingEntry(sIdx, idx, $event)"
                  @remove="removePricingEntry(sIdx, idx)"
                />
              </div>
            </section>

            <USeparator />

            <!-- Account Stats Pricing Rules (per-platform, always visible) -->
            <section class="space-y-3">
              <div class="flex items-center justify-between">
                <h4 class="text-sm font-medium text-highlighted">
                  {{ t('admin.channels.form.accountStatsPricingRules') }}
                </h4>
                <UButton icon="i-lucide-plus" color="neutral" variant="ghost" size="xs" :label="t('admin.channels.form.addRule')" @click="addAccountStatsRule(sIdx)" />
              </div>

              <!-- Filter rules for this platform's groups -->
              <p v-if="section.account_stats_pricing_rules.length === 0" class="text-xs text-muted">
                {{ t('admin.channels.form.noRulesConfigured') }}
              </p>

              <div
                v-for="(rule, ruleIndex) in section.account_stats_pricing_rules"
                :key="ruleIndex"
                class="space-y-4 rounded-lg p-4 ring ring-inset ring-default"
              >
                <div class="flex items-center justify-between gap-2">
                  <UInput
                    v-model="rule.name"
                    variant="none"
                    class="min-w-0 flex-1"
                    :ui="{ base: 'px-0 text-sm font-medium text-highlighted' }"
                    :placeholder="t('admin.channels.form.ruleName')"
                  />
                  <UButton
                    icon="i-lucide-trash-2"
                    color="error"
                    variant="ghost"
                    size="xs"
                    :label="t('common.delete')"
                    @click="removeAccountStatsRule(sIdx, ruleIndex)"
                  />
                </div>

                <UFormField :label="t('admin.channels.form.ruleGroups')" size="sm" :ui="ruleFieldUi">
                  <div class="flex flex-wrap gap-x-4 gap-y-2 pt-0.5">
                    <UCheckbox
                      v-for="gid in section.group_ids"
                      :key="gid"
                      size="sm"
                      :label="getGroupNameById(gid)"
                      :model-value="rule.group_ids.includes(gid)"
                      @update:model-value="rule.group_ids.includes(gid) ? rule.group_ids.splice(rule.group_ids.indexOf(gid), 1) : rule.group_ids.push(gid)"
                    />
                  </div>
                  <p v-if="section.group_ids.length === 0" class="text-xs text-muted">
                    {{ t('admin.channels.form.noGroupsInChannel') }}
                  </p>
                </UFormField>

                <UFormField
                  :label="t('admin.channels.form.ruleAccounts')"
                  :help="t('admin.channels.form.ruleAccountsHint')"
                  size="sm"
                  :ui="ruleFieldUi"
                >
                  <!-- Selected account chips -->
                  <div v-if="rule.account_ids.length > 0" class="mb-1.5 flex flex-wrap gap-1">
                    <UBadge
                      v-for="accountId in rule.account_ids"
                      :key="accountId"
                      color="neutral"
                      variant="subtle"
                      class="gap-0.5 pe-0.5"
                    >
                      {{ getRuleAccountLabel(accountId) }}
                      <UButton
                        icon="i-lucide-x"
                        color="neutral"
                        variant="link"
                        size="xs"
                        class="p-0.5"
                        :ui="{ leadingIcon: 'size-3' }"
                        :aria-label="t('common.remove')"
                        @click="removeRuleAccount(rule, accountId)"
                      />
                    </UBadge>
                  </div>
                  <!-- Account search input -->
                  <div class="relative rule-account-search-container">
                    <UInput
                      :model-value="ruleAccountSearchKeyword[`${section.platform}-${ruleIndex}`]"
                      icon="i-lucide-search"
                      type="text"
                      size="sm"
                      class="w-full"
                      :placeholder="t('admin.channels.form.searchAccountPlaceholder')"
                      @update:model-value="updateRuleAccountSearch(section.platform, ruleIndex, $event)"
                      @focus="onRuleAccountSearchFocus(section.platform, ruleIndex)"
                    />
                    <!-- Search results dropdown -->
                    <div
                      v-if="showRuleAccountDropdown[`${section.platform}-${ruleIndex}`] && (ruleAccountSearchResults[`${section.platform}-${ruleIndex}`]?.length ?? 0) > 0"
                      class="absolute z-50 mt-1 max-h-48 w-full overflow-auto rounded-md bg-default p-1 shadow-lg ring ring-default"
                    >
                      <UButton
                        v-for="account in ruleAccountSearchResults[`${section.platform}-${ruleIndex}`]"
                        :key="account.id"
                        color="neutral"
                        variant="ghost"
                        size="sm"
                        block
                        class="justify-start"
                        :disabled="rule.account_ids.includes(account.id)"
                        @click="selectRuleAccount(rule, account, section.platform, ruleIndex)"
                      >
                        <span>{{ account.name }}</span>
                        <span class="text-xs text-dimmed">#{{ account.id }}</span>
                      </UButton>
                    </div>
                  </div>
                </UFormField>

                <div class="space-y-2">
                  <div class="flex items-center justify-between">
                    <span class="text-xs font-medium text-muted">{{ t('admin.channels.form.ruleModelPricing') }}</span>
                    <UButton icon="i-lucide-plus" color="neutral" variant="ghost" size="xs" :label="t('common.add')" @click="addRulePricingEntry(sIdx, ruleIndex)" />
                  </div>
                  <p v-if="rule.pricing.length === 0" class="text-xs text-muted">
                    {{ t('admin.channels.form.noPricingRules') }}
                  </p>
                  <div v-else class="space-y-2">
                    <PricingEntryCard
                      v-for="(entry, pIdx) in rule.pricing"
                      :key="pIdx"
                      :entry="entry"
                      :platform="section.platform"
                      @update="rule.pricing.splice(pIdx, 1, $event)"
                      @remove="removeRulePricingEntry(sIdx, ruleIndex, pIdx)"
                    />
                  </div>
                </div>
              </div>
            </section>
          </div>
        </UForm>
      </div>

      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton
            color="neutral"
            variant="subtle"
            :label="t('common.cancel', 'Cancel')"
            @click="closeDialog"
          />
          <UButton
            type="submit"
            form="channel-form"
            :disabled="submitting"
            :loading="submitting"
            :label="submitting
              ? t('common.submitting', 'Submitting...')
              : editingChannel
                ? t('common.update', 'Update')
                : t('common.create', 'Create')"
          />
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.channels.deleteChannel', 'Delete Channel')"
      :message="deleteConfirmMessage"
      :confirm-text="t('common.delete', 'Delete')"
      :cancel-text="t('common.cancel', 'Cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { fetchPaginatedItems } from '@/utils/paginatedItems'
import { adminAPI } from '@/api/admin'
import type { Channel, ChannelModelPricing, CreateChannelRequest, UpdateChannelRequest, AccountStatsPricingRule } from '@/api/admin/channels'
import type { PricingFormEntry } from '@/components/admin/channel/types'
import { apiIntervalsToForm, apiTimePricingToForm, createDefaultTimePricingForm, findModelConflict, formIntervalsToAPI, formTimePricingToAPI, isValidPositiveMultiplier, mTokToPerToken, perTokenToMTok, validateIntervals, validateTimePricing } from '@/components/admin/channel/types'
import type { DropdownMenuItem, TabsItem } from '@nuxt/ui'
import type { AdminGroup, GroupPlatform } from '@/types'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Select from '@/components/common/Select.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import AdminRowAction from '@/components/admin/page/AdminRowAction.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import PricingEntryCard from '@/components/admin/channel/PricingEntryCard.vue'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { useKeyedDebouncedSearch } from '@/composables/useKeyedDebouncedSearch'

const { t } = useI18n()
const appStore = useAppStore()

// Web Search global enabled state (loaded once on mount)
const webSearchGlobalEnabled = ref(false)
async function loadWebSearchGlobalState() {
  try {
    const cfg = await adminAPI.settings.getWebSearchEmulationConfig()
    webSearchGlobalEnabled.value = cfg?.enabled === true && (cfg?.providers?.length ?? 0) > 0
  } catch (err: unknown) {
    console.warn('Failed to load web search global state:', err)
    webSearchGlobalEnabled.value = false
  }
}

// ── Form-level pricing rule type (per-platform) ──
interface FormPricingRule {
  name: string
  group_ids: number[]
  account_ids: number[]
  pricing: PricingFormEntry[]
}

// ── Platform Section type ──
interface PlatformSection {
  platform: GroupPlatform
  enabled: boolean
  collapsed: boolean
  group_ids: number[]
  model_mapping: Record<string, string>
  model_pricing: PricingFormEntry[]
  web_search_emulation: boolean
  codex_image_generation_bridge: boolean
  bedrock_cc_compat: boolean
  account_stats_pricing_rules: FormPricingRule[]
}

// ── Table columns ──
const allColumns = computed<Column[]>(() => [
  { key: 'name', label: t('admin.channels.columns.name', 'Name'), sortable: true },
  { key: 'description', label: t('admin.channels.columns.description', 'Description'), sortable: false },
  { key: 'status', label: t('admin.channels.columns.status', 'Status'), sortable: true },
  { key: 'group_count', label: t('admin.channels.columns.groups', 'Groups'), sortable: false },
  { key: 'pricing_count', label: t('admin.channels.columns.pricing', 'Pricing'), sortable: false },
  { key: 'created_at', label: t('admin.channels.columns.createdAt', 'Created'), sortable: true },
  { key: 'actions', label: t('admin.channels.columns.actions', 'Actions'), sortable: false }
])

// ── Column visibility ("显示列"), persisted per browser ──
const ALWAYS_VISIBLE_COLUMNS = new Set(['name', 'actions'])
const HIDDEN_COLUMNS_KEY = 'channel-hidden-columns'

function loadHiddenColumns(): string[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(HIDDEN_COLUMNS_KEY) || '[]')
    return Array.isArray(parsed) ? parsed.filter((key): key is string => typeof key === 'string') : []
  } catch {
    return []
  }
}

const hiddenColumns = ref<string[]>(loadHiddenColumns())

function setColumnVisible(key: string, visible: boolean) {
  hiddenColumns.value = visible
    ? hiddenColumns.value.filter(k => k !== key)
    : [...new Set([...hiddenColumns.value, key])]
  try {
    localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify(hiddenColumns.value))
  } catch {
    // Storage may be unavailable (private mode); the choice still applies for this session.
  }
}

const columns = computed<Column[]>(() =>
  allColumns.value.filter(col => ALWAYS_VISIBLE_COLUMNS.has(col.key) || !hiddenColumns.value.includes(col.key))
)

const columnMenuItems = computed<DropdownMenuItem[]>(() =>
  allColumns.value
    .filter(col => !ALWAYS_VISIBLE_COLUMNS.has(col.key))
    .map(col => ({
      label: col.label,
      type: 'checkbox' as const,
      checked: !hiddenColumns.value.includes(col.key),
      onUpdateChecked: (checked: boolean) => setColumnVisible(col.key, checked),
      onSelect: (e: Event) => e.preventDefault()
    }))
)

// Field layouts shared by the dialog's toggle rows and the stats-rule sub-fields.
const toggleFieldUi = { root: 'items-center gap-6', description: 'text-xs', container: 'shrink-0' }
const ruleFieldUi = { label: 'text-muted', help: 'mt-1.5 text-xs' }

const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.channels.allStatus', 'All Status') },
  { value: 'active', label: t('admin.channels.statusActive', 'Active') },
  { value: 'disabled', label: t('admin.channels.statusDisabled', 'Disabled') }
])

const statusEditOptions = computed(() => [
  { value: 'active', label: t('admin.channels.statusActive', 'Active') },
  { value: 'disabled', label: t('admin.channels.statusDisabled', 'Disabled') }
])

const billingModelSourceOptions = computed(() => [
  { value: 'channel_mapped', label: t('admin.channels.form.billingModelSourceChannelMapped', 'Bill by channel-mapped model') },
  { value: 'requested', label: t('admin.channels.form.billingModelSourceRequested', 'Bill by requested model') },
  { value: 'upstream', label: t('admin.channels.form.billingModelSourceUpstream', 'Bill by final upstream model') },
  { value: 'response_model', label: t('admin.channels.form.billingModelSourceResponse', 'Bill by upstream response model') }
])

// ── State ──
const channels = ref<Channel[]>([])
const loading = ref(false)
const searchQuery = ref('')
const filters = reactive({ status: '' })
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0
})
const sortState = reactive({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc'
})

// Dialog state
const showDialog = ref(false)
const editingChannel = ref<Channel | null>(null)
const submitting = ref(false)
const showDeleteDialog = ref(false)
const deletingChannel = ref<Channel | null>(null)
const activeTab = ref<string>('basic')

// Groups
const allGroups = ref<AdminGroup[]>([])
const groupsLoading = ref(false)

// All channels for group-conflict detection (independent of current page)
const allChannelsForConflict = ref<Channel[]>([])

// Form data
const form = reactive({
  name: '',
  description: '',
  status: 'active',
  restrict_models: false,
  billing_model_source: 'channel_mapped' as string,
  platforms: [] as PlatformSection[],
  apply_pricing_to_account_stats: false,
})

let abortController: AbortController | null = null

// ── Platform config ──
const platformOrder: GroupPlatform[] = ['anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax']
// Composite pricing/mapping may target every concrete schedulable provider.
const compositePlatforms: GroupPlatform[] = ['anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax']

// ── Helpers ──
function formatDate(value: string): string {
  if (!value) return '-'
  return new Date(value).toLocaleDateString()
}

// ── Platform section helpers ──
const activePlatforms = computed(() => form.platforms.filter(s => s.enabled).map(s => s.platform))

// Dialog tabs: basic settings first, then one tab per enabled platform.
const dialogTabs = computed<(TabsItem & { platform?: GroupPlatform })[]>(() => [
  { label: t('admin.channels.form.basicSettings'), value: 'basic' },
  ...form.platforms
    .filter(s => s.enabled)
    .map(s => ({
      label: t('admin.groups.platforms.' + s.platform, s.platform),
      value: s.platform,
      platform: s.platform
    }))
])

function addPlatformSection(platform: GroupPlatform) {
  form.platforms.push({
    platform,
    enabled: true,
    collapsed: false,
    group_ids: [],
    model_mapping: {},
    model_pricing: [],
    web_search_emulation: false,
    codex_image_generation_bridge: false,
    bedrock_cc_compat: false,
    account_stats_pricing_rules: [],
  })
}

function togglePlatform(platform: GroupPlatform) {
  const section = form.platforms.find(s => s.platform === platform)
  if (section) {
    section.enabled = !section.enabled
    if (!section.enabled && activeTab.value === platform) {
      activeTab.value = 'basic'
    }
  } else {
    addPlatformSection(platform)
  }
}

function getGroupsForPlatform(platform: GroupPlatform): AdminGroup[] {
  return allGroups.value.filter(
    g => g.platform === platform || (g.platform === 'composite' && compositePlatforms.includes(platform))
  )
}

// ── Group helpers ──
const groupToChannelMap = computed(() => {
  const map = new Map<number, Channel>()
  for (const ch of allChannelsForConflict.value) {
    if (editingChannel.value && ch.id === editingChannel.value.id) continue
    for (const gid of ch.group_ids || []) {
      map.set(gid, ch)
    }
  }
  return map
})

function isGroupInOtherChannel(groupId: number, _platform: string): boolean {
  return groupToChannelMap.value.has(groupId)
}

function getGroupChannelName(groupId: number): string {
  return groupToChannelMap.value.get(groupId)?.name || ''
}

function getGroupInOtherChannelLabel(groupId: number): string {
  const name = getGroupChannelName(groupId)
  return t('admin.channels.form.inOtherChannel', { name }, `In "${name}"`)
}

const deleteConfirmMessage = computed(() => {
  const name = deletingChannel.value?.name || ''
  return t(
    'admin.channels.deleteConfirm',
    { name },
    `Are you sure you want to delete channel "${name}"? This action cannot be undone.`
  )
})

function toggleGroupInSection(sectionIdx: number, groupId: number) {
  const section = form.platforms[sectionIdx]
  const idx = section.group_ids.indexOf(groupId)
  if (idx >= 0) {
    section.group_ids.splice(idx, 1)
  } else {
    section.group_ids.push(groupId)
  }
}

// ── Pricing helpers ──
function addPricingEntry(sectionIdx: number) {
  form.platforms[sectionIdx].model_pricing.push({
    models: [],
    billing_mode: 'token',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_write_1h_price: null,
    cache_read_price: null,
    fast_multiplier: null,
    flex_multiplier: null,
    max_reasoning_effort_multiplier: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: createDefaultTimePricingForm()
  })
}

const syncingPlatform = ref<string | null>(null)

async function syncLatestModels(sectionIdx: number) {
  const platform = form.platforms[sectionIdx].platform
  if (syncingPlatform.value) return
  syncingPlatform.value = platform
  try {
    const result = await adminAPI.channels.syncPricingModels(platform)
    // Collect all model names already present in this platform's pricing entries
    const existingModels = new Set<string>()
    for (const entry of form.platforms[sectionIdx].model_pricing) {
      for (const m of entry.models) existingModels.add(m)
    }
    const newModels = result.models.filter(m => !existingModels.has(m))
    if (newModels.length === 0) {
      appStore.showSuccess(t('admin.channels.form.syncModelsAlreadyUpToDate'))
      return
    }
    // Add new models as a single new pricing entry (user fills in prices)
    form.platforms[sectionIdx].model_pricing.push({
      models: newModels,
      billing_mode: 'token',
      input_price: null,
      output_price: null,
      cache_write_price: null,
      cache_write_1h_price: null,
      cache_read_price: null,
      fast_multiplier: null,
      flex_multiplier: null,
      max_reasoning_effort_multiplier: null,
      image_input_price: null,
      image_output_price: null,
      per_request_price: null,
      intervals: [],
      time_pricing: createDefaultTimePricingForm()
    })
    appStore.showSuccess(t('admin.channels.form.syncModelsSuccess', { count: newModels.length }))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.channels.form.syncModelsError')))
  } finally {
    syncingPlatform.value = null
  }
}

function updatePricingEntry(sectionIdx: number, idx: number, updated: PricingFormEntry) {
  form.platforms[sectionIdx].model_pricing.splice(idx, 1, updated)
}

function removePricingEntry(sectionIdx: number, idx: number) {
  form.platforms[sectionIdx].model_pricing.splice(idx, 1)
}

// ── Model Mapping helpers ──
function addMappingEntry(sectionIdx: number) {
  const mapping = form.platforms[sectionIdx].model_mapping
  let key = ''
  let i = 1
  while (key === '' || key in mapping) {
    key = `model-${i}`
    i++
  }
  mapping[key] = ''
}

function removeMappingEntry(sectionIdx: number, key: string) {
  delete form.platforms[sectionIdx].model_mapping[key]
}

function renameMappingKey(sectionIdx: number, oldKey: string, newKey: string) {
  newKey = newKey.trim()
  if (!newKey || newKey === oldKey) return
  const mapping = form.platforms[sectionIdx].model_mapping
  if (newKey in mapping) return
  const value = mapping[oldKey]
  delete mapping[oldKey]
  mapping[newKey] = value
}

// ── Account Stats Pricing helpers ──
function addAccountStatsRule(sectionIdx: number) {
  form.platforms[sectionIdx].account_stats_pricing_rules.push({
    name: '',
    group_ids: [],
    account_ids: [],
    pricing: []
  })
}

function addRulePricingEntry(sectionIdx: number, ruleIndex: number) {
  form.platforms[sectionIdx].account_stats_pricing_rules[ruleIndex].pricing.push({
    models: [],
    billing_mode: 'token',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_write_1h_price: null,
    cache_read_price: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: createDefaultTimePricingForm()
  })
}

function removeAccountStatsRule(sectionIdx: number, ruleIndex: number) {
  form.platforms[sectionIdx].account_stats_pricing_rules.splice(ruleIndex, 1)
  // Clear all search state since indices shift after removal
  ruleAccountSearchRunner.clearAll()
  clearAllRuleAccountSearchState()
}

function removeRulePricingEntry(sectionIdx: number, ruleIndex: number, pricingIndex: number) {
  form.platforms[sectionIdx].account_stats_pricing_rules[ruleIndex].pricing.splice(pricingIndex, 1)
}

function getGroupNameById(groupId: number): string {
  const group = allGroups.value.find(g => g.id === groupId)
  return group ? group.name : `#${groupId}`
}

// ── Account search for pricing rules ──
interface SimpleAccount { id: number; name: string; platform: string }

const ruleAccountSearchKeyword = ref<Record<string, string>>({})
const ruleAccountSearchResults = ref<Record<string, SimpleAccount[]>>({})
const showRuleAccountDropdown = ref<Record<string, boolean>>({})
// Cache: account ID → name, populated when search results are selected
const ruleAccountNameCache = ref<Record<number, string>>({})

const ruleAccountSearchRunner = useKeyedDebouncedSearch<SimpleAccount[]>({
  delay: 300,
  search: async (keyword, { key, signal }) => {
    const platform = key.split('-')[0]
    const res = await adminAPI.accounts.list(1, 20, { platform, search: keyword }, { signal })
    return res.items.map(a => ({ id: a.id, name: a.name, platform: a.platform }))
  },
  onSuccess: (key, result) => { ruleAccountSearchResults.value[key] = result },
  onError: (key) => { ruleAccountSearchResults.value[key] = [] },
})

// Store the keyword first, then search: the same order the native v-model + @input had.
function updateRuleAccountSearch(platform: string, ruleIndex: number, value: string | number | null | undefined) {
  ruleAccountSearchKeyword.value[`${platform}-${ruleIndex}`] = String(value ?? '')
  onRuleAccountSearchInput(platform, ruleIndex)
}

function onRuleAccountSearchInput(platform: string, ruleIndex: number) {
  const key = `${platform}-${ruleIndex}`
  showRuleAccountDropdown.value[key] = true
  ruleAccountSearchRunner.trigger(key, ruleAccountSearchKeyword.value[key] || '')
}

function onRuleAccountSearchFocus(platform: string, ruleIndex: number) {
  const key = `${platform}-${ruleIndex}`
  showRuleAccountDropdown.value[key] = true
  if (!ruleAccountSearchResults.value[key]?.length) {
    ruleAccountSearchRunner.trigger(key, ruleAccountSearchKeyword.value[key] || '')
  }
}

function selectRuleAccount(
  rule: { account_ids: number[] },
  account: SimpleAccount,
  platform: string,
  ruleIndex: number,
) {
  if (!rule.account_ids.includes(account.id)) {
    rule.account_ids.push(account.id)
    ruleAccountNameCache.value[account.id] = account.name
  }
  const key = `${platform}-${ruleIndex}`
  ruleAccountSearchKeyword.value[key] = ''
  showRuleAccountDropdown.value[key] = false
}

function removeRuleAccount(rule: { account_ids: number[] }, accountId: number) {
  const idx = rule.account_ids.indexOf(accountId)
  if (idx !== -1) rule.account_ids.splice(idx, 1)
}

function getRuleAccountLabel(accountId: number): string {
  const name = ruleAccountNameCache.value[accountId]
  return name ? `${name} #${accountId}` : `#${accountId}`
}

function handleRuleAccountClickOutside(event: MouseEvent) {
  const target = event.target as HTMLElement
  if (!target.closest('.rule-account-search-container')) {
    Object.keys(showRuleAccountDropdown.value).forEach(key => {
      showRuleAccountDropdown.value[key] = false
    })
  }
}

function clearAllRuleAccountSearchState() {
  ruleAccountSearchKeyword.value = {}
  ruleAccountSearchResults.value = {}
  showRuleAccountDropdown.value = {}
}

function accountStatsRulesToAPI(): AccountStatsPricingRule[] {
  const rules: AccountStatsPricingRule[] = []
  for (const section of form.platforms) {
    if (!section.enabled) continue
    for (const rule of section.account_stats_pricing_rules) {
      rules.push({
        name: rule.name,
        group_ids: rule.group_ids,
        account_ids: rule.account_ids,
        pricing: rule.pricing
          .filter(p => p.models.length > 0)
          .map(p => ({
            platform: section.platform,
            models: p.models,
            billing_mode: p.billing_mode,
            input_price: mTokToPerToken(p.input_price),
            output_price: mTokToPerToken(p.output_price),
            cache_write_price: mTokToPerToken(p.cache_write_price),
            cache_write_1h_price: mTokToPerToken(p.cache_write_1h_price),
            cache_read_price: mTokToPerToken(p.cache_read_price),
            image_input_price: mTokToPerToken(p.image_input_price),
            image_output_price: mTokToPerToken(p.image_output_price),
            per_request_price: p.per_request_price != null && p.per_request_price !== '' ? Number(p.per_request_price) : null,
            intervals: formIntervalsToAPI(p.intervals || []),
            time_pricing: null
          }))
      })
    }
  }
  return rules
}

// ── Form ↔ API conversion ──
function formToAPI(): { group_ids: number[], model_pricing: ChannelModelPricing[], model_mapping: Record<string, Record<string, string>>, features_config: Record<string, unknown> } {
  const group_ids: number[] = []
  const model_pricing: ChannelModelPricing[] = []
  const model_mapping: Record<string, Record<string, string>> = {}
  // Preserve existing features_config fields not managed by the form
  const featuresConfig: Record<string, unknown> = editingChannel.value?.features_config
    ? { ...editingChannel.value.features_config }
    : {}

  for (const section of form.platforms) {
    if (!section.enabled) continue
    group_ids.push(...section.group_ids)

    // Model mapping per platform
    if (Object.keys(section.model_mapping).length > 0) {
      model_mapping[section.platform] = { ...section.model_mapping }
    }

    // Model pricing with platform tag
    for (const entry of section.model_pricing) {
      if (entry.models.length === 0) continue
      model_pricing.push({
        platform: section.platform,
        models: entry.models,
        billing_mode: entry.billing_mode,
        input_price: mTokToPerToken(entry.input_price),
        output_price: mTokToPerToken(entry.output_price),
        cache_write_price: mTokToPerToken(entry.cache_write_price),
        cache_write_1h_price: mTokToPerToken(entry.cache_write_1h_price),
        cache_read_price: mTokToPerToken(entry.cache_read_price),
        fast_multiplier: entry.fast_multiplier != null && entry.fast_multiplier !== '' ? Number(entry.fast_multiplier) : null,
        flex_multiplier: entry.flex_multiplier != null && entry.flex_multiplier !== '' ? Number(entry.flex_multiplier) : null,
        max_reasoning_effort_multiplier: entry.max_reasoning_effort_multiplier != null && entry.max_reasoning_effort_multiplier !== '' ? Number(entry.max_reasoning_effort_multiplier) : null,
        image_input_price: mTokToPerToken(entry.image_input_price),
        image_output_price: mTokToPerToken(entry.image_output_price),
        per_request_price: entry.per_request_price != null && entry.per_request_price !== '' ? Number(entry.per_request_price) : null,
        intervals: formIntervalsToAPI(entry.intervals || []),
        time_pricing: formTimePricingToAPI(entry.time_pricing)
      })
    }
  }
  const uniqueGroupIds = Array.from(new Set(group_ids))

  // Collect web_search_emulation (only anthropic platform supports it)
  // Always write the key so that disabling in the UI correctly sets platform to false,
  // rather than leaving a stale true value from the cloned features_config.
  const wsEmulation: Record<string, boolean> = {}
  for (const section of form.platforms) {
    if (!section.enabled) continue
    if (section.platform === 'anthropic') {
      wsEmulation[section.platform] = !!section.web_search_emulation
    }
  }
  if (Object.keys(wsEmulation).length > 0) {
    featuresConfig.web_search_emulation = wsEmulation
  } else {
    delete featuresConfig.web_search_emulation
  }

  const codexImageGenerationBridge: Record<string, boolean> = {}
  for (const section of form.platforms) {
    if (!section.enabled) continue
    if (section.platform === 'openai') {
      codexImageGenerationBridge[section.platform] = !!section.codex_image_generation_bridge
    }
  }
  if (Object.keys(codexImageGenerationBridge).length > 0) {
    featuresConfig.codex_image_generation_bridge = codexImageGenerationBridge
  } else {
    delete featuresConfig.codex_image_generation_bridge
  }

  const bedrockCCCompat: Record<string, boolean> = {}
  for (const section of form.platforms) {
    if (!section.enabled) continue
    if (section.platform === 'anthropic') {
      bedrockCCCompat[section.platform] = !!section.bedrock_cc_compat
    }
  }
  if (Object.keys(bedrockCCCompat).length > 0) {
    featuresConfig.bedrock_cc_compat = bedrockCCCompat
  } else {
    delete featuresConfig.bedrock_cc_compat
  }

  return { group_ids: uniqueGroupIds, model_pricing, model_mapping, features_config: featuresConfig }
}

function apiToForm(channel: Channel): PlatformSection[] {
  // Build a map: groupID → platform
  const groupPlatformMap = new Map<number, GroupPlatform>()
  for (const g of allGroups.value) {
    groupPlatformMap.set(g.id, g.platform)
  }

  // Determine which platforms are active (from groups + pricing + mapping)
  const activePlatforms = new Set<GroupPlatform>()
  for (const gid of channel.group_ids || []) {
    const p = groupPlatformMap.get(gid)
    if (p === 'composite') {
      compositePlatforms.forEach(platform => activePlatforms.add(platform))
    } else if (p) {
      activePlatforms.add(p)
    }
  }
  for (const p of channel.model_pricing || []) {
    if (p.platform) activePlatforms.add(p.platform as GroupPlatform)
  }
  for (const p of Object.keys(channel.model_mapping || {})) {
    if (platformOrder.includes(p as GroupPlatform)) activePlatforms.add(p as GroupPlatform)
  }

  // Build sections in platform order
  const sections: PlatformSection[] = []
  for (const platform of platformOrder) {
    if (!activePlatforms.has(platform)) continue

    const groupIds = (channel.group_ids || []).filter(gid => {
      const groupPlatform = groupPlatformMap.get(gid)
      return groupPlatform === platform ||
        (groupPlatform === 'composite' && compositePlatforms.includes(platform))
    })
    const mapping = (channel.model_mapping || {})[platform] || {}
    const pricing = (channel.model_pricing || [])
      .filter(p => (p.platform || 'anthropic') === platform)
      .map(p => ({
        models: p.models || [],
        billing_mode: p.billing_mode,
        input_price: perTokenToMTok(p.input_price),
        output_price: perTokenToMTok(p.output_price),
        cache_write_price: perTokenToMTok(p.cache_write_price),
        cache_write_1h_price: perTokenToMTok(p.cache_write_1h_price),
        cache_read_price: perTokenToMTok(p.cache_read_price),
        fast_multiplier: p.fast_multiplier,
        flex_multiplier: p.flex_multiplier,
        max_reasoning_effort_multiplier: p.max_reasoning_effort_multiplier,
        image_input_price: perTokenToMTok(p.image_input_price),
        image_output_price: perTokenToMTok(p.image_output_price),
        per_request_price: p.per_request_price,
        intervals: apiIntervalsToForm(p.intervals || []),
        time_pricing: apiTimePricingToForm(p.time_pricing)
      } as PricingFormEntry))

    // Read web_search_emulation from features_config
    const fc = channel.features_config
    const wsEmulation = fc?.web_search_emulation as Record<string, boolean> | undefined
    const webSearchEnabled = wsEmulation?.[platform] === true
    const codexImageGenerationBridge = fc?.codex_image_generation_bridge as Record<string, boolean> | undefined
    const codexImageGenerationBridgeEnabled = codexImageGenerationBridge?.[platform] === true
    const bedrockCCCompatEnabled = fc?.bedrock_cc_compat === true

    sections.push({
      platform,
      enabled: true,
      collapsed: false,
      group_ids: groupIds,
      model_mapping: { ...mapping },
      model_pricing: pricing,
      web_search_emulation: webSearchEnabled,
      codex_image_generation_bridge: codexImageGenerationBridgeEnabled,
      bedrock_cc_compat: bedrockCCCompatEnabled,
      account_stats_pricing_rules: [],
    })
  }

  return sections
}

// ── Load data ──
async function loadChannels() {
  if (abortController) abortController.abort()
  const ctrl = new AbortController()
  abortController = ctrl
  loading.value = true

  try {
    const response = await adminAPI.channels.list(pagination.page, pagination.page_size, {
      status: filters.status || undefined,
      search: searchQuery.value || undefined,
      sort_by: sortState.sort_by,
      sort_order: sortState.sort_order
    }, { signal: ctrl.signal })

    if (ctrl.signal.aborted || abortController !== ctrl) return
    channels.value = response.items || []
    pagination.total = response.total
  } catch (error: unknown) {
    const e = error as { name?: string; code?: string }
    if (e?.name === 'AbortError' || e?.code === 'ERR_CANCELED') return
    appStore.showError(extractApiErrorMessage(error, t('admin.channels.loadError', 'Failed to load channels')))
  } finally {
    if (abortController === ctrl) {
      loading.value = false
      abortController = null
    }
  }
}

async function loadGroups() {
  groupsLoading.value = true
  try {
    allGroups.value = await adminAPI.groups.getAll()
  } catch (error) {
    console.error('Error loading groups:', error)
  } finally {
    groupsLoading.value = false
  }
}

async function loadAllChannelsForConflict() {
  try {
    allChannelsForConflict.value = await fetchPaginatedItems(
      (page, size) => adminAPI.channels.list(page, size), 1000, 1000
    )
  } catch (error) {
    // Fallback to current page data
    allChannelsForConflict.value = channels.value
  }
}

let searchTimeout: ReturnType<typeof setTimeout>
function handleSearch() {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadChannels()
  }, 300)
}

function handlePageChange(page: number) {
  pagination.page = page
  loadChannels()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page_size = pageSize
  pagination.page = 1
  loadChannels()
}

function handleSort(key: string, order: 'asc' | 'desc') {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadChannels()
}

// ── Dialog ──
function resetForm() {
  form.name = ''
  form.description = ''
  form.status = 'active'
  form.restrict_models = false
  form.billing_model_source = 'channel_mapped'
  form.platforms = []
  form.apply_pricing_to_account_stats = false
  activeTab.value = 'basic'
  ruleAccountSearchRunner.clearAll()
  clearAllRuleAccountSearchState()
  ruleAccountNameCache.value = {}
}

async function openCreateDialog() {
  editingChannel.value = null
  resetForm()
  await Promise.all([loadGroups(), loadAllChannelsForConflict()])
  showDialog.value = true
}

async function openEditDialog(channel: Channel) {
  editingChannel.value = channel
  form.name = channel.name
  form.description = channel.description || ''
  form.status = channel.status
  form.restrict_models = channel.restrict_models || false
  form.billing_model_source = channel.billing_model_source || 'channel_mapped'
  form.apply_pricing_to_account_stats = channel.apply_pricing_to_account_stats || false
  // Must load groups first so apiToForm can map groupID → platform
  await Promise.all([loadGroups(), loadAllChannelsForConflict()])
  form.platforms = apiToForm(channel)

  // Distribute channel-level rules into per-platform sections
  distributeRulesToPlatforms(channel.account_stats_pricing_rules || [])

  // Populate ruleAccountNameCache for existing rule accounts
  await populateRuleAccountNameCache()

  showDialog.value = true
}

/** Distribute flat channel-level rules into the matching platform section based on group_ids */
function distributeRulesToPlatforms(apiRules: AccountStatsPricingRule[]) {
  // Build groupID → platform lookup
  const groupPlatformMap = new Map<number, GroupPlatform>()
  for (const g of allGroups.value) {
    groupPlatformMap.set(g.id, g.platform)
  }

  for (const apiRule of apiRules) {
    // Infer platform from group_ids
    const platforms = new Set<GroupPlatform>()
    for (const gid of apiRule.group_ids || []) {
      const p = groupPlatformMap.get(gid)
      if (p && p !== 'composite') platforms.add(p)
    }
    // If pricing has a platform field, use that as fallback
    if (platforms.size === 0 && apiRule.pricing?.length > 0) {
      const p = apiRule.pricing[0].platform as GroupPlatform | undefined
      if (p) platforms.add(p)
    }
    const targetPlatform = platforms.size >= 1 ? [...platforms][0] : null
    if (!targetPlatform) continue

    const section = form.platforms.find(s => s.platform === targetPlatform)
    if (!section) continue

    const formRule: FormPricingRule = {
      name: apiRule.name || '',
      group_ids: [...(apiRule.group_ids || [])],
      account_ids: [...(apiRule.account_ids || [])],
      pricing: (apiRule.pricing || []).map(p => ({
        models: [...(p.models || [])],
        billing_mode: p.billing_mode,
        input_price: perTokenToMTok(p.input_price),
        output_price: perTokenToMTok(p.output_price),
        cache_write_price: perTokenToMTok(p.cache_write_price),
        cache_write_1h_price: perTokenToMTok(p.cache_write_1h_price),
        cache_read_price: perTokenToMTok(p.cache_read_price),
        image_input_price: perTokenToMTok(p.image_input_price),
        image_output_price: perTokenToMTok(p.image_output_price),
        per_request_price: p.per_request_price,
        intervals: apiIntervalsToForm(p.intervals || []),
        time_pricing: createDefaultTimePricingForm()
      } as PricingFormEntry))
    }
    section.account_stats_pricing_rules.push(formRule)
  }
}

/** Populate ruleAccountNameCache by fetching account details for all account_ids in rules */
async function populateRuleAccountNameCache() {
  const allAccountIds = new Set<number>()
  for (const section of form.platforms) {
    for (const rule of section.account_stats_pricing_rules) {
      for (const id of rule.account_ids) {
        allAccountIds.add(id)
      }
    }
  }
  if (allAccountIds.size === 0) return

  // Fetch account details in parallel (batch of individual getById calls)
  const ids = [...allAccountIds]
  const results = await Promise.allSettled(
    ids.map(id => adminAPI.accounts.getById(id))
  )
  for (let i = 0; i < ids.length; i++) {
    const result = results[i]
    if (result.status === 'fulfilled') {
      ruleAccountNameCache.value[ids[i]] = result.value.name
    }
    // If rejected, the cache won't have the name, so it'll show "#ID" which is acceptable
  }
}

function closeDialog() {
  showDialog.value = false
  editingChannel.value = null
  resetForm()
}

async function handleSubmit() {
  if (submitting.value) return
  if (!form.name.trim()) {
    appStore.showError(t('admin.channels.nameRequired', 'Please enter a channel name'))
    return
  }

  // Check for pricing entries with empty models (would be silently skipped)
  for (const section of form.platforms.filter(s => s.enabled)) {
    if (section.group_ids.length === 0) {
      const platformLabel = t('admin.groups.platforms.' + section.platform, section.platform)
      appStore.showError(t('admin.channels.noGroupsSelected', { platform: platformLabel }))
      activeTab.value = section.platform
      return
    }
    for (const entry of section.model_pricing) {
      if (entry.models.length === 0) {
        const platformLabel = t('admin.groups.platforms.' + section.platform, section.platform)
        appStore.showError(t('admin.channels.emptyModelsInPricing', { platform: platformLabel }))
        activeTab.value = section.platform
        return
      }
    }
  }

  // Check model pattern conflicts per platform (duplicate / wildcard overlap)
  for (const section of form.platforms.filter(s => s.enabled)) {
    // Collect all pricing models for this platform
    const allModels: string[] = []
    for (const entry of section.model_pricing) {
      allModels.push(...entry.models)
    }
    const pricingConflict = findModelConflict(allModels)
    if (pricingConflict) {
      appStore.showError(
        t('admin.channels.modelConflict',
          { model1: pricingConflict[0], model2: pricingConflict[1] })
      )
      activeTab.value = section.platform
      return
    }
    // Check model mapping source pattern conflicts
    const mappingKeys = Object.keys(section.model_mapping)
    if (mappingKeys.length > 0) {
      const mappingConflict = findModelConflict(mappingKeys)
      if (mappingConflict) {
        appStore.showError(
          t('admin.channels.mappingConflict',
            { model1: mappingConflict[0], model2: mappingConflict[1] })
        )
        activeTab.value = section.platform
        return
      }
    }
  }

  // 校验 per_request/image 模式必须有价格 (只校验启用的平台)
  for (const section of form.platforms.filter(s => s.enabled)) {
    for (const entry of section.model_pricing) {
      if (entry.models.length === 0) continue
      if ((entry.billing_mode === 'per_request' || entry.billing_mode === 'image') &&
          (entry.per_request_price == null || entry.per_request_price === '') &&
          (!entry.intervals || entry.intervals.length === 0)) {
        appStore.showError(t('admin.channels.form.perRequestPriceRequired'))
        return
      }
    }
  }

  // 校验区间合法性（范围、重叠等）
  for (const section of form.platforms.filter(s => s.enabled)) {
    for (const entry of section.model_pricing) {
      if (!isValidPositiveMultiplier(entry.fast_multiplier) ||
          !isValidPositiveMultiplier(entry.flex_multiplier) ||
          !isValidPositiveMultiplier(entry.max_reasoning_effort_multiplier)) {
        const platformLabel = t('admin.groups.platforms.' + section.platform, section.platform)
        const modelLabel = entry.models.join(', ') || t('admin.channels.form.unnamed')
        appStore.showError(`${platformLabel} - ${modelLabel}: ${t('admin.channels.form.multiplierPositive')}`)
        activeTab.value = section.platform
        return
      }
      if (!entry.intervals || entry.intervals.length === 0) continue
      const intervalErr = validateIntervals(entry.intervals, entry.billing_mode, t)
      if (intervalErr) {
        const platformLabel = t('admin.groups.platforms.' + section.platform, section.platform)
        const modelLabel = entry.models.join(', ') || t('admin.channels.form.unnamed')
        appStore.showError(`${platformLabel} - ${modelLabel}: ${intervalErr}`)
        activeTab.value = section.platform
        return
      }
    }
  }

  // 校验时间段定价，并切换到对应平台便于修正
  for (const section of form.platforms.filter(s => s.enabled)) {
    for (const entry of section.model_pricing) {
      const timePricingError = validateTimePricing(entry.time_pricing, t)
      if (timePricingError) {
        const platformLabel = t('admin.groups.platforms.' + section.platform, section.platform)
        const modelLabel = entry.models.join(', ') || t('admin.channels.form.unnamed')
        appStore.showError(`${platformLabel} - ${modelLabel}: ${timePricingError}`)
        activeTab.value = section.platform
        return
      }
    }
  }

  const { group_ids, model_pricing, model_mapping, features_config } = formToAPI()

  submitting.value = true
  try {
    if (editingChannel.value) {
      const req: UpdateChannelRequest = {
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        status: form.status,
        group_ids,
        model_pricing,
        model_mapping: Object.keys(model_mapping).length > 0 ? model_mapping : {},
        billing_model_source: form.billing_model_source,
        restrict_models: form.restrict_models,
        features_config,
        apply_pricing_to_account_stats: form.apply_pricing_to_account_stats,
        account_stats_pricing_rules: accountStatsRulesToAPI()
      }
      await adminAPI.channels.update(editingChannel.value.id, req)
      appStore.showSuccess(t('admin.channels.updateSuccess', 'Channel updated'))
    } else {
      const req: CreateChannelRequest = {
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        group_ids,
        model_pricing,
        model_mapping: Object.keys(model_mapping).length > 0 ? model_mapping : {},
        billing_model_source: form.billing_model_source,
        restrict_models: form.restrict_models,
        features_config,
        apply_pricing_to_account_stats: form.apply_pricing_to_account_stats,
        account_stats_pricing_rules: accountStatsRulesToAPI()
      }
      await adminAPI.channels.create(req)
      appStore.showSuccess(t('admin.channels.createSuccess', 'Channel created'))
    }
    closeDialog()
    loadChannels()
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, editingChannel.value
      ? t('admin.channels.updateError', 'Failed to update channel')
      : t('admin.channels.createError', 'Failed to create channel')))
  } finally {
    submitting.value = false
  }
}

// ── Toggle status ──
async function toggleChannelStatus(channel: Channel) {
  const newStatus = channel.status === 'active' ? 'disabled' : 'active'
  try {
    await adminAPI.channels.update(channel.id, { status: newStatus })
    if (filters.status && filters.status !== newStatus) {
      // Item no longer matches the active filter — reload list
      await loadChannels()
    } else {
      channel.status = newStatus
    }
  } catch (error) {
    appStore.showError(t('admin.channels.updateError', 'Failed to update channel'))
    console.error('Error toggling channel status:', error)
  }
}

// ── Delete ──
function handleDelete(channel: Channel) {
  deletingChannel.value = channel
  showDeleteDialog.value = true
}

async function confirmDelete() {
  if (!deletingChannel.value) return

  try {
    await adminAPI.channels.remove(deletingChannel.value.id)
    appStore.showSuccess(t('admin.channels.deleteSuccess', 'Channel deleted'))
    showDeleteDialog.value = false
    deletingChannel.value = null
    loadChannels()
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('admin.channels.deleteError', 'Failed to delete channel')))
  }
}

// ── Lifecycle ──
onMounted(() => {
  loadChannels()
  loadGroups()
  loadWebSearchGlobalState()
  document.addEventListener('click', handleRuleAccountClickOutside)
})

onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
  document.removeEventListener('click', handleRuleAccountClickOutside)
  ruleAccountSearchRunner.clearAll()
  clearAllRuleAccountSearchState()
})
</script>

<style scoped>
@reference "../../style.css";
.channel-dialog-body {
  display: flex;
  flex-direction: column;
  height: 70vh;
  min-height: 400px;
}

</style>
