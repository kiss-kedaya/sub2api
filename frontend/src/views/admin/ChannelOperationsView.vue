<template>
  <AppLayout>
    <div class="flex min-w-0 flex-col gap-6">
      <AdminPageHeader :title="t('channelOps.title')" :description="t('channelOps.description')">
        <template #actions>
          <UPopover :content="{ align: 'end' }">
            <UButton color="neutral" variant="ghost" icon="i-lucide-info" :label="t('channelOps.scopeTitle')" />
            <template #content><p class="max-w-sm p-3 text-xs leading-5 text-muted">{{ t('channelOps.scope') }}</p></template>
          </UPopover>
          <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :loading="loading" :disabled="loading || busy" :label="t('channelOps.refresh')" @click="refresh" />
        </template>
      </AdminPageHeader>
      <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />
      <UAlert v-if="notice" role="status" color="success" variant="subtle" icon="i-lucide-circle-check" :description="notice" />
      <p v-if="loading" role="status" class="text-sm text-muted">{{ t('channelOps.loading') }}</p>
      <UAlert v-if="snapshotStale" role="status" color="warning" variant="subtle" icon="i-lucide-history" :description="t('channelOps.previousData')" />
      <fieldset :disabled="busy || loading" class="flex min-w-0 flex-col gap-6">
        <section class="flex flex-col gap-3">
          <form class="flex flex-wrap items-center gap-1.5" @submit.prevent="channelPage = 1; refresh()">
            <UInput v-model="channelSearch" icon="i-lucide-search" class="w-full sm:w-64" maxlength="100" :placeholder="t('channelOps.search')" :aria-label="t('channelOps.search')" />
            <UButton type="submit" color="neutral" variant="outline" :label="t('channelOps.apply')" />
            <UButton type="button" color="neutral" variant="ghost" icon="i-lucide-layout-grid" :label="t('channelOps.overview')" @click="selectChannel(0)" />
            <div class="ml-auto flex flex-wrap items-center gap-1.5">
              <UButton to="/admin/channels/pricing" color="neutral" variant="ghost" trailing-icon="i-lucide-arrow-up-right" :label="t('channelOps.pricing')" />
              <UButton icon="i-lucide-plus" data-testid="new-channel" :label="t('channelOps.createChannel')" @click="editChannel(null)" />
            </div>
          </form>
          <UAlert v-if="channelsError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="channelsError" />
          <div class="flex flex-wrap items-center gap-1.5" :aria-label="t('channelOps.channels')">
            <UButton
              v-for="channel in channels"
              :key="channel.id"
              size="sm"
              :color="channelId === channel.id ? 'primary' : 'neutral'"
              :variant="channelId === channel.id ? 'solid' : 'outline'"
              :aria-pressed="channelId === channel.id"
              @click="selectChannel(channel.id)"
            >{{ channel.name }} · {{ channel.status }}</UButton>
            <span v-if="!channels.length && !channelsError" class="text-sm text-muted">{{ t('channelOps.empty') }}</span>
            <div class="ml-auto flex items-center gap-1.5 text-xs text-muted">
              <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-chevron-left" :aria-label="t('channelOps.previous')" :disabled="channelPage <= 1" @click="channelPage--; refresh()" />
              <span class="tabular-nums">{{ channelPage }} · {{ channelTotal }}</span>
              <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-chevron-right" :aria-label="t('channelOps.next')" :disabled="channelPage * pageSize >= channelTotal" @click="channelPage++; refresh()" />
            </div>
          </div>
          <div v-if="selectedChannel && selectedChannel.id === channelId" class="flex flex-wrap items-center gap-x-3 gap-y-1.5 rounded-lg bg-elevated/50 px-3 py-2 text-sm">
            <strong class="text-highlighted">{{ selectedChannel.name }} #{{ channelId }}</strong>
            <span class="text-muted">{{ t('channelOps.groups') }}: {{ selectedChannel.group_ids?.join(', ') || '—' }}</span>
            <div class="ml-auto flex flex-wrap items-center gap-1">
              <UButton color="neutral" variant="ghost" size="sm" icon="i-lucide-pencil" data-testid="edit-channel" :label="t('channelOps.edit')" @click="editChannel(selectedChannel)" />
              <UButton color="error" variant="ghost" size="sm" icon="i-lucide-trash-2" data-testid="delete-channel" :label="t('channelOps.deleteChannel')" @click="pending = { kind: 'channel', channel: selectedChannel }" />
              <RouterLink class="px-2 text-sm text-muted underline-offset-4 hover:text-default hover:underline" :to="{ path: '/admin/channels/pricing', query: { edit_channel: channelId } }">{{ t('channelOps.fullConfig') }}</RouterLink>
            </div>
          </div>
        </section>
        <UCollapsible v-model:open="filtersOpen" :unmount-on-hide="false" class="flex flex-col gap-3">
          <UButton color="neutral" variant="ghost" class="self-start" icon="i-lucide-sliders-horizontal" :trailing-icon="filtersOpen ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'" :label="t('channelOps.advancedFilters')" />
          <template #content>
            <UForm :state="filterState" class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" @submit="applyFilters">
              <UFormField :label="t('channelOps.from')" required><UInput v-model="from" class="w-full" type="datetime-local" required /></UFormField>
              <UFormField :label="t('channelOps.until')" required><UInput v-model="until" class="w-full" type="datetime-local" required /></UFormField>
              <UFormField :label="t('channelOps.groups')"><UInput v-model.number="groupId" class="w-full" type="number" min="0" step="1" /></UFormField>
              <UFormField :label="t('channelOps.modelScope')"><USelect v-model="modelScope" class="w-full" data-testid="model-scope" :items="modelScopeItems" /></UFormField>
              <UFormField :label="t('channelOps.model')"><UInput v-model="model" class="w-full" maxlength="200" /></UFormField>
              <div class="flex items-end"><UButton type="submit" :label="t('channelOps.apply')" /></div>
            </UForm>
          </template>
        </UCollapsible>
        <div class="flex flex-col gap-1.5">
          <nav class="flex flex-wrap items-center gap-1 text-sm" :aria-label="t('channelOps.drilldown')">
            <UButton color="neutral" variant="ghost" size="sm" icon="i-lucide-layout-grid" :label="t('channelOps.overview')" @click="selectChannel(0)" />
            <template v-if="channelId"><UIcon name="i-lucide-chevron-right" class="size-4 text-dimmed" /><UButton color="neutral" variant="ghost" size="sm" @click="selectChannel(channelId)">{{ selectedChannel?.name || `#${channelId}` }}</UButton></template>
            <template v-if="upstreamId"><UIcon name="i-lucide-chevron-right" class="size-4 text-dimmed" /><UButton color="neutral" variant="ghost" size="sm" @click="accountId = 0; statsPage = 1; qualityBefore = 0; refresh()">{{ upstreamLabel(upstreamId) }}</UButton></template>
            <template v-if="accountId"><UIcon name="i-lucide-chevron-right" class="size-4 text-dimmed" /><span class="px-2 text-highlighted">{{ t('channelOps.account') }} #{{ accountId }}</span></template>
          </nav>
          <p v-if="snapshot" class="text-xs leading-5 text-muted" data-testid="applied-scope">{{ t('channelOps.appliedScope') }}: {{ date(snapshot.from) }} → {{ date(snapshot.until) }} · {{ t('channelOps.channels') }} #{{ snapshot.scope.channel_id || '*' }} · {{ t('channelOps.upstream') }} #{{ snapshot.scope.upstream_id || '*' }} · {{ t('channelOps.account') }} #{{ snapshot.scope.account_id || '*' }} · {{ t('channelOps.groups') }} {{ snapshot.scope.group_id || '*' }} · {{ t('channelOps.modelScope') }} {{ snapshot.scope.model_scope }} · {{ snapshot.scope.model || '*' }}</p>
        </div>
        <section v-if="snapshot" class="flex flex-col gap-3" data-testid="usage-section">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 class="text-base font-semibold text-highlighted">{{ t('channelOps.latency') }}</h2>
              <p class="mt-0.5 text-xs text-muted">{{ t('channelOps.latencyNote') }}</p>
            </div>
            <UButton v-if="channelId && !accountId && !upstreamId" color="neutral" variant="outline" size="sm" icon="i-lucide-arrow-left-right" @click="accountMode = !accountMode; statsPage = 1; refresh()">{{ t(accountMode ? 'channelOps.byUpstream' : 'channelOps.byAccount') }}</UButton>
          </div>
          <UAlert v-if="snapshot.usage.status !== 'ready'" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="snapshot.usage.error" />
          <template v-else-if="snapshot.usage.data">
            <ChannelOperationsMetrics :metrics="snapshot.usage.data.summary" />
            <p class="text-xs text-muted">{{ t('channelOps.queried') }} {{ date(snapshot.usage.queried_at) }} · {{ t('channelOps.latest') }} {{ date(snapshot.usage.data.summary.latest_sample_at) }}</p>
            <p v-if="!snapshot.usage.data.summary.requests" class="text-sm text-muted" data-testid="no-usage">{{ t('channelOps.noUsage') }}</p>
            <UTable :data="snapshot.usage.data.rows" :columns="usageColumns" :ui="tableUi">
              <template #name-cell="{ row }"><UButton v-if="!accountId && (row.original.dimension_id || dimension === 'upstream')" variant="link" class="p-0" @click="drill(row.original.dimension_id)">{{ row.original.name }}</UButton><span v-else class="text-highlighted">{{ row.original.name }}</span></template>
              <template #samples-cell="{ row }">{{ row.original.samples }} / {{ row.original.unknown_samples }}</template>
              <template #latency-cell="{ row }"><span class="tabular-nums">{{ ms(row.original.p50_ms) }} / {{ ms(row.original.p90_ms) }} / {{ ms(row.original.p95_ms) }}</span></template>
            </UTable>
            <div class="flex items-center justify-end gap-1.5 text-xs text-muted">
              <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-chevron-left" :disabled="statsPage <= 1" :label="t('channelOps.previous')" @click="statsPage--; refresh()" />
              <span class="px-1 tabular-nums">{{ statsPage }} · {{ snapshot.usage.data.total }}</span>
              <UButton data-testid="usage-next" color="neutral" variant="outline" size="sm" trailing-icon="i-lucide-chevron-right" :disabled="statsPage * pageSize >= snapshot.usage.data.total" :label="t('channelOps.next')" @click="statsPage++; refresh()" />
            </div>
            <UCollapsible class="flex flex-col gap-2">
              <UButton color="neutral" variant="ghost" size="sm" class="self-start" trailing-icon="i-lucide-chevron-down">{{ t('channelOps.lineage') }} ({{ snapshot.usage.data.lineage.length }} / {{ snapshot.usage.data.lineage_total }})</UButton>
              <template #content>
                <p class="mb-2 text-xs text-muted">{{ t('channelOps.lineageNote') }}</p>
                <UTable :data="snapshot.usage.data.lineage" :columns="lineageColumns" :ui="tableUi" />
              </template>
            </UCollapsible>
          </template>
          <p class="text-xs text-muted">{{ t('channelOps.errorNote') }} <RouterLink class="text-default underline underline-offset-4" to="/admin/ops">{{ t('channelOps.errors') }}</RouterLink></p>
        </section>
        <USeparator v-if="snapshot" />
        <section v-if="snapshot" class="flex flex-col gap-3" data-testid="quality-section">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div>
              <h2 class="text-base font-semibold text-highlighted">{{ t('channelOps.quality') }}</h2>
              <p class="mt-0.5 text-xs text-muted">{{ t('channelOps.qualityNote') }}</p>
            </div>
            <RouterLink class="text-sm text-muted underline-offset-4 hover:text-default hover:underline" to="/admin/account-quality">{{ t('channelOps.qualityEditor') }}</RouterLink>
          </div>
          <UAlert v-if="snapshot.quality.status !== 'ready'" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="snapshot.quality.error" />
          <template v-else-if="snapshot.quality.data">
            <div class="flex flex-wrap items-center gap-1.5 text-sm">
              <UBadge color="neutral" variant="subtle">{{ t('channelOps.rounds') }} {{ snapshot.quality.data.summary.rounds }}</UBadge>
              <UBadge color="success" variant="subtle">{{ t('channelOps.passed') }} {{ snapshot.quality.data.summary.passed }}</UBadge>
              <UBadge color="error" variant="subtle">{{ t('channelOps.degraded') }} {{ snapshot.quality.data.summary.degraded }}</UBadge>
              <UBadge color="warning" variant="subtle">{{ t('channelOps.inconclusive') }} {{ snapshot.quality.data.summary.inconclusive }}</UBadge>
              <span class="ml-1 text-xs text-muted">{{ t('channelOps.queried') }} {{ date(snapshot.quality.queried_at) }} · {{ t('channelOps.latest') }} {{ date(snapshot.quality.data.summary.latest_sample_at) }}</span>
            </div>
            <p v-if="!snapshot.quality.data.summary.rounds" class="text-sm text-muted">{{ t('channelOps.noQuality') }}</p>
            <UTable :data="snapshot.quality.data.rows" :columns="qualityColumns" :ui="tableUi">
              <template #observed-cell="{ row }">{{ date(row.original.observed_at) }}</template>
              <template #account-cell="{ row }"><UButton variant="link" class="p-0" @click="accountId = row.original.account_id; statsPage = 1; qualityBefore = 0; refresh()">#{{ row.original.account_id }}</UButton></template>
              <template #model-cell="{ row }">{{ row.original.measured_model || '—' }}</template>
              <template #outcome-cell="{ row }"><UBadge :color="row.original.outcome === 'degraded' ? 'error' : row.original.outcome === 'passed' ? 'success' : 'warning'" variant="subtle" size="sm">{{ t(`channelOps.${row.original.outcome}`) }}</UBadge><span v-if="!row.original.round_complete" class="mt-1 block text-xs text-warning">{{ t('channelOps.incompleteRound') }} {{ row.original.round_probes }} / {{ row.original.round_expected ?? '—' }}</span></template>
              <template #probes-cell="{ row }">{{ row.original.passed_probes }} / {{ row.original.probes }} ({{ row.original.expected ?? '—' }})</template>
              <template #actions-cell="{ row }">{{ row.original.actions.filter(Boolean).join(', ') || '—' }}</template>
              <template #plan-cell="{ row }"><RouterLink class="text-default underline underline-offset-4" :to="{ path: '/admin/account-quality', query: { plan_id: row.original.plan_id } }">#{{ row.original.plan_id }}</RouterLink></template>
            </UTable>
            <div class="flex justify-end gap-1.5">
              <UButton color="neutral" variant="outline" size="sm" :disabled="!qualityBefore" :label="t('channelOps.latestPage')" @click="qualityBefore = 0; refresh()" />
              <UButton color="neutral" variant="outline" size="sm" :disabled="!snapshot.quality.data.has_more" :label="t('channelOps.older')" @click="olderQuality" />
            </div>
          </template>
        </section>
        <USeparator v-if="snapshot" />
        <div v-if="snapshot" class="grid gap-6 lg:grid-cols-3">
          <section class="flex flex-col gap-3 lg:col-span-2" data-testid="errors-section">
            <div>
              <h2 class="text-base font-semibold text-highlighted">{{ t('channelOps.errors') }}</h2>
              <p class="mt-0.5 text-xs text-muted">{{ t('channelOps.errorsScope') }}</p>
            </div>
            <UAlert v-if="snapshot.errors.status !== 'ready'" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="snapshot.errors.error" />
            <template v-else-if="snapshot.errors.data">
              <div class="flex flex-wrap items-center gap-1.5 text-sm">
                <UBadge color="neutral" variant="subtle">{{ t('channelOps.errorRecords') }} {{ snapshot.errors.data.records }}</UBadge>
                <UBadge color="error" variant="subtle">{{ t('channelOps.upstream5xx') }} {{ snapshot.errors.data.upstream_5xx }}</UBadge>
                <span class="ml-1 text-xs text-muted">{{ snapshot.errors.data.source }} · {{ t('channelOps.queried') }} {{ date(snapshot.errors.queried_at) }} · {{ t('channelOps.latest') }} {{ date(snapshot.errors.data.latest_sample_at) }}</span>
              </div>
              <p v-if="!snapshot.errors.data.records" class="text-sm text-muted">{{ t('channelOps.noErrors') }}</p>
              <UTable :data="snapshot.errors.data.rows" :columns="errorColumns" :ui="tableUi" />
              <p class="text-xs text-muted">{{ snapshot.errors.data.rows.length }} / {{ snapshot.errors.data.bucket_total }} · {{ t('channelOps.topErrors') }}</p>
            </template>
          </section>
          <section class="flex flex-col gap-3">
            <div>
              <h2 class="text-base font-semibold text-highlighted">{{ t('channelOps.capacity') }}</h2>
              <p class="mt-0.5 text-xs text-muted">{{ t('channelOps.capacityNote') }}</p>
            </div>
            <UAlert v-if="snapshot.capacity.status !== 'ready'" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="snapshot.capacity.error" />
            <UPageCard v-else-if="snapshot.capacity.data" variant="subtle" :ui="{ container: 'gap-y-2 sm:p-4' }">
              <dl class="grid grid-cols-3 gap-3">
                <div><dt class="text-xs text-muted">{{ t('channelOps.accounts') }}</dt><dd class="text-xl font-semibold tabular-nums text-highlighted">{{ snapshot.capacity.data.accounts }}</dd></div>
                <div><dt class="text-xs text-muted">{{ t('channelOps.schedulable') }}</dt><dd class="text-xl font-semibold tabular-nums text-highlighted">{{ snapshot.capacity.data.schedulable }}</dd></div>
                <div><dt class="text-xs text-muted">{{ t('channelOps.concurrency') }}</dt><dd class="text-xl font-semibold tabular-nums text-highlighted">{{ snapshot.capacity.data.configured_concurrency }}</dd></div>
              </dl>
              <p class="text-xs text-muted">{{ t('channelOps.queried') }} {{ date(snapshot.capacity.queried_at) }}</p>
            </UPageCard>
          </section>
        </div>
        <USeparator v-if="channelId" />
        <section v-if="channelId" class="flex flex-col gap-3">
          <div>
            <h2 class="text-base font-semibold text-highlighted">{{ t('channelOps.membership') }}</h2>
            <p class="mt-0.5 text-xs text-muted">{{ t('channelOps.membershipNote') }}</p>
          </div>
          <UAlert v-if="catalogError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="catalogError" />
          <fieldset v-if="catalog" :disabled="catalogStale || catalogChannelId !== channelId" class="grid min-w-0 gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
            <div class="flex min-w-0 flex-col gap-3">
              <form class="flex flex-wrap items-center gap-1.5" @submit.prevent="upstreamPage = 1; refresh()">
                <UInput v-model="upstreamSearch" icon="i-lucide-search" class="min-w-0 flex-1" maxlength="100" :placeholder="t('channelOps.searchUpstream')" :aria-label="t('channelOps.searchUpstream')" />
                <UButton type="submit" color="neutral" variant="outline" :label="t('channelOps.apply')" />
                <UButton type="button" icon="i-lucide-plus" :label="t('channelOps.createUpstream')" @click="dialogError = ''; upstreamEdit = { id: 0, name: '' }" />
              </form>
              <ul class="flex flex-col divide-y divide-default">
                <li v-for="upstream in catalog.upstreams" :key="upstream.id" class="flex items-center gap-2 py-1.5">
                  <UButton variant="link" class="min-w-0 flex-1 justify-start p-0" @click="upstreamId = upstream.id; accountId = 0; statsPage = 1; qualityBefore = 0; refresh()">{{ upstream.name }} (#{{ upstream.id }}) · {{ upstream.members }}</UButton>
                  <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-pencil" :aria-label="t('channelOps.rename')" @click="dialogError = ''; upstreamEdit = { id: upstream.id, name: upstream.name }" />
                  <UButton color="error" variant="ghost" size="xs" icon="i-lucide-trash-2" :aria-label="t('channelOps.deleteUpstream')" @click="queueMutation({ action: 'delete_upstream', upstream_id: upstream.id, confirm: true })" />
                </li>
              </ul>
              <p v-if="!catalog.upstreams.length" class="text-sm text-muted">{{ t('channelOps.noUpstream') }}</p>
              <div class="flex items-center justify-end gap-1.5 text-xs text-muted">
                <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-chevron-left" :disabled="upstreamPage <= 1" :label="t('channelOps.previous')" @click="upstreamPage--; refresh()" />
                <span class="px-1 tabular-nums">{{ upstreamPage }} · {{ catalog.upstream_total }}</span>
                <UButton data-testid="upstream-next" color="neutral" variant="outline" size="sm" trailing-icon="i-lucide-chevron-right" :disabled="upstreamPage * pageSize >= catalog.upstream_total" :label="t('channelOps.next')" @click="upstreamPage++; refresh()" />
              </div>
            </div>
            <div class="flex min-w-0 flex-col gap-3">
              <form class="flex flex-wrap items-center gap-1.5" @submit.prevent="memberPage = 1; refresh()">
                <UInput v-model="memberSearch" icon="i-lucide-search" class="min-w-0 flex-1" maxlength="100" :placeholder="t('channelOps.searchAccount')" :aria-label="t('channelOps.searchAccount')" />
                <USelect v-model="membership" class="w-36" data-testid="membership" :items="membershipItems" :aria-label="t('channelOps.members')" />
                <UButton type="submit" color="neutral" variant="outline" :label="t('channelOps.apply')" />
              </form>
              <UTable :data="catalog.accounts" :columns="memberColumns" :ui="tableUi">
                <template #account-cell="{ row }"><UButton variant="link" class="p-0" @click="accountId = row.original.id; upstreamId = 0; statsPage = 1; qualityBefore = 0; refresh()">{{ row.original.name }} #{{ row.original.id }}</UButton><p class="text-xs text-muted">{{ row.original.platform }}</p></template>
                <template #state-cell="{ row }">{{ row.original.status }} · {{ row.original.concurrency }}<p v-if="!row.original.eligible" class="text-xs text-warning">{{ t('channelOps.ineligible') }}</p></template>
                <template #upstream-cell="{ row }">{{ upstreamLabel(row.original.upstream_id) }}</template>
                <template #actions-cell="{ row }">
                  <div class="flex justify-end gap-1">
                    <UButton color="neutral" variant="outline" size="xs" :disabled="!row.original.eligible" :label="t('channelOps.assign')" @click="dialogError = ''; assigning = row.original; targetUpstream = 0" />
                    <UButton v-if="row.original.upstream_id" color="error" variant="ghost" size="xs" :label="t('channelOps.detach')" @click="queueMutation({ action: 'detach', account_id: row.original.id, expected_upstream_id: row.original.upstream_id, confirm: true })" />
                  </div>
                </template>
              </UTable>
              <p v-if="!catalog.accounts.length" class="text-sm text-muted">{{ t('channelOps.empty') }}</p>
              <div class="flex items-center justify-end gap-1.5 text-xs text-muted">
                <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-chevron-left" :disabled="memberPage <= 1" :label="t('channelOps.previous')" @click="memberPage--; refresh()" />
                <span class="px-1 tabular-nums">{{ memberPage }} · {{ catalog.total }} · revision {{ catalog.revision }}</span>
                <UButton data-testid="member-next" color="neutral" variant="outline" size="sm" trailing-icon="i-lucide-chevron-right" :disabled="memberPage * pageSize >= catalog.total" :label="t('channelOps.next')" @click="memberPage++; refresh()" />
              </div>
            </div>
          </fieldset>
        </section>
      </fieldset>
      <BaseDialog :show="!!channelEdit" :title="t(channelEdit?.id ? 'channelOps.edit' : 'channelOps.createChannel')" @close="!busy && (channelEdit = null)">
        <form v-if="channelEdit" id="channel-ops-channel-form" class="flex flex-col gap-4" @submit.prevent="saveChannel">
          <fieldset :disabled="busy" class="flex flex-col gap-4">
            <UFormField :label="t('channelOps.name')" required><UInput v-model="channelEdit.name" class="w-full" required maxlength="100" /></UFormField>
            <UFormField :label="t('channelOps.descriptionField')"><UTextarea v-model="channelEdit.description" class="w-full" :rows="3" autoresize maxlength="2000" /></UFormField>
            <UFormField v-if="channelEdit.id" :label="t('channelOps.channelStatus')"><USelect v-model="channelEdit.status" class="w-48" :items="channelStatusItems" /></UFormField>
            <p class="text-xs text-muted">{{ t('channelOps.basicEdit') }}</p>
            <UAlert v-if="dialogError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="dialogError" />
          </fieldset>
        </form>
        <template #footer>
          <div class="flex justify-end gap-2">
            <UButton color="neutral" variant="outline" :disabled="busy" :label="t('channelOps.cancel')" @click="channelEdit = null" />
            <UButton type="submit" form="channel-ops-channel-form" :loading="busy" :label="t('channelOps.save')" />
          </div>
        </template>
      </BaseDialog>
      <BaseDialog :show="!!upstreamEdit" :title="t('channelOps.upstream')" @close="!busy && (upstreamEdit = null)">
        <form v-if="upstreamEdit" id="channel-ops-upstream-form" class="flex flex-col gap-4" @submit.prevent="saveUpstream">
          <UFormField :label="t('channelOps.name')" required><UInput v-model="upstreamEdit.name" class="w-full" required maxlength="100" :disabled="busy" /></UFormField>
          <UAlert v-if="dialogError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="dialogError" />
        </form>
        <template #footer>
          <div class="flex justify-end gap-2">
            <UButton color="neutral" variant="outline" :disabled="busy" :label="t('channelOps.cancel')" @click="upstreamEdit = null" />
            <UButton type="submit" form="channel-ops-upstream-form" :disabled="busy" :loading="busy" :label="t('channelOps.save')" />
          </div>
        </template>
      </BaseDialog>
      <BaseDialog :show="!!assigning" :title="t('channelOps.assign')" @close="!busy && (assigning = null)">
        <div class="flex flex-col gap-4">
          <div>
            <p class="font-medium text-highlighted">{{ assigning?.name }} #{{ assigning?.id }}</p>
            <p class="mt-0.5 text-xs text-muted">{{ t('channelOps.targetHint') }}</p>
          </div>
          <form class="flex flex-wrap items-center gap-1.5" @submit.prevent="upstreamPage = 1; refresh()">
            <UInput v-model="upstreamSearch" icon="i-lucide-search" class="min-w-0 flex-1" maxlength="100" :placeholder="t('channelOps.searchUpstream')" :aria-label="t('channelOps.searchUpstream')" />
            <UButton type="submit" color="neutral" variant="outline" :disabled="loading" :label="t('channelOps.apply')" />
          </form>
          <div v-if="catalog" class="flex flex-col gap-3">
            <URadioGroup v-model="targetUpstream" data-testid="target-upstream" name="target-upstream" :items="targetItems" />
            <div class="flex items-center gap-1.5 text-xs text-muted">
              <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-chevron-left" :disabled="loading || upstreamPage <= 1" :label="t('channelOps.previous')" @click="upstreamPage--; refresh()" />
              <span class="px-1 tabular-nums">{{ upstreamPage }} · {{ catalog.upstream_total }}</span>
              <UButton color="neutral" variant="outline" size="sm" trailing-icon="i-lucide-chevron-right" :disabled="loading || upstreamPage * pageSize >= catalog.upstream_total" :label="t('channelOps.next')" @click="upstreamPage++; refresh()" />
            </div>
          </div>
          <UFormField :label="t('channelOps.target')" :help="t('channelOps.reassignNote')"><UInput v-model.number="targetUpstream" class="w-full" type="number" min="1" step="1" :disabled="busy" /></UFormField>
          <UAlert v-if="dialogError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="dialogError" />
        </div>
        <template #footer>
          <div class="flex justify-end gap-2">
            <UButton color="neutral" variant="outline" :disabled="busy" :label="t('channelOps.cancel')" @click="assigning = null" />
            <UButton :disabled="busy || loading || !catalog || !targetUpstream" :loading="busy" :label="t('channelOps.save')" @click="assignMember" />
          </div>
        </template>
      </BaseDialog>
      <ConfirmDialog :show="!!pending" :title="t('channelOps.confirmTitle')" :message="confirmationMessage" :confirm-text="t('channelOps.confirm')" :danger="true" @cancel="!busy && (pending = null)" @confirm="confirmPending" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import type { TableColumn } from '@nuxt/ui'
import { templateTableUi } from '@/views/admin/ops/utils/templateUi'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import { channelOpsMessages } from '@/components/admin/operations/channelOpsMessages'
import { keepSuccessfulSections } from '@/components/admin/operations/channelOperationsState'
import ChannelOperationsMetrics from '@/components/admin/operations/ChannelOperationsMetrics.vue'
import * as channelsAPI from '@/api/admin/channels'
import * as ops from '@/api/admin/channelOperations'
import type { Channel } from '@/api/admin/channels'

const { t } = useI18n({ useScope: 'local', messages: channelOpsMessages }), route = useRoute(), router = useRouter(), pageSize = 20
const channelId = ref(positiveId(route.query.channel_id)), upstreamId = ref(route.query.upstream_id === '-1' ? -1 : positiveId(route.query.upstream_id)), accountId = ref(positiveId(route.query.account_id))
const channels = ref<Channel[]>([]), selectedChannel = ref<Channel | null>(null), channelTotal = ref(0), channelPage = ref(1), channelSearch = ref('')
const catalog = ref<ops.Catalog | null>(null), snapshot = ref<ops.Snapshot | null>(null)
const catalogChannelId = ref(0), catalogStale = ref(false), snapshotStale = ref(false)
const memberPage = ref(1), memberSearch = ref(''), upstreamPage = ref(1), upstreamSearch = ref(''), membership = ref('all'), statsPage = ref(1), qualityBefore = ref(0), accountMode = ref(false)
const from = ref(localDate(new Date(Date.now() - 24 * 3600_000))), until = ref(localDate(new Date())), groupId = ref(0), model = ref(''), modelScope = ref('public')
const loading = ref(false), busy = ref(false), error = ref(''), notice = ref(''), channelsError = ref(''), catalogError = ref(''), dialogError = ref('')
const channelEdit = ref<{ id: number; name: string; description: string; status: string } | null>(null), upstreamEdit = ref<{ id: number; name: string } | null>(null), assigning = ref<ops.Member | null>(null), targetUpstream = ref(0)
type Pending = { kind: 'channel'; channel: Channel } | { kind: 'mutation'; body: ops.Mutation; channelId: number }
const pending = ref<Pending | null>(null)
const confirmationMessage = computed(() => {
  const action = pending.value
  if (!action) return ''
  if (action.kind === 'channel') return `${action.channel.name} #${action.channel.id} · ${t('channelOps.deleteChannelNote')}`
  const m = action.body
  const title = t(m.action === 'delete_upstream' ? 'channelOps.deleteUpstream' : m.action === 'detach' ? 'channelOps.detach' : 'channelOps.assign')
  const target = m.action === 'delete_upstream' ? `${upstreamLabel(m.upstream_id || 0)} #${m.upstream_id}` : `${t('channelOps.account')} #${m.account_id}: ${upstreamLabel(m.expected_upstream_id || 0)} #${m.expected_upstream_id || '—'} → ${upstreamLabel(m.upstream_id || 0)} #${m.upstream_id || '—'}`
  return `${title} · ${t('channelOps.channels')} #${action.channelId} · ${target} · ${t('channelOps.confirmMembership')}`
})
const dimension = computed(() => accountId.value ? 'account' : !channelId.value ? 'channel' : upstreamId.value || accountMode.value ? 'account' : 'upstream')
const tableUi = templateTableUi
const filtersOpen = ref(false)
const filterState = computed(() => ({ from: from.value, until: until.value, groupId: groupId.value, model: model.value, modelScope: modelScope.value }))
const modelScopeItems = computed(() => [{ value: 'public', label: t('channelOps.public') }, { value: 'recorded', label: t('channelOps.recorded') }, { value: 'upstream', label: t('channelOps.upstreamModel') }])
const membershipItems = computed(() => [{ value: 'all', label: t('channelOps.all') }, { value: 'assigned', label: t('channelOps.assigned') }, { value: 'unassigned', label: t('channelOps.unassigned') }])
const channelStatusItems = computed(() => [{ value: 'active', label: t('channelOps.channelActive') }, { value: 'disabled', label: t('channelOps.channelDisabled') }])
const targetItems = computed(() => (catalog.value?.upstreams ?? []).map(target => ({ value: target.id, label: `${target.name} #${target.id}` })))
const usageColumns = computed<TableColumn<ops.UsageRow>[]>(() => [
  { id: 'name', header: t('channelOps.dimension') },
  { accessorKey: 'requests', header: t('channelOps.requests') },
  { id: 'samples', header: `${t('channelOps.known')} / ${t('channelOps.unknown')}` },
  { id: 'latency', header: 'P50 / P90 / P95 (ms)' }
])
const lineageColumns = computed<TableColumn<ops.Lineage>[]>(() => [
  { accessorKey: 'public_model', header: t('channelOps.public') },
  { accessorKey: 'recorded_model', header: t('channelOps.recorded') },
  { id: 'upstream_model', header: t('channelOps.upstreamModel'), cell: ({ row }) => row.original.upstream_model || '—' },
  { id: 'chain', header: t('channelOps.chain'), cell: ({ row }) => row.original.model_mapping_chain || '—' },
  { accessorKey: 'samples', header: t('channelOps.requests') }
])
const qualityColumns = computed<TableColumn<ops.QualityRound>[]>(() => [
  { id: 'observed', header: t('channelOps.time') }, { id: 'account', header: t('channelOps.account') }, { id: 'model', header: t('channelOps.model') },
  { id: 'outcome', header: t('channelOps.outcome') }, { id: 'probes', header: t('channelOps.probes') }, { id: 'actions', header: t('channelOps.actions') }, { id: 'plan', header: t('channelOps.plan') }
])
const errorColumns = computed<TableColumn<ops.ErrorLogs['rows'][number]>[]>(() => [
  { accessorKey: 'error_type', header: t('channelOps.errorType') }, { accessorKey: 'error_phase', header: t('channelOps.errorPhase') },
  { id: 'status', header: 'HTTP', cell: ({ row }) => row.original.status_code || '—' }, { accessorKey: 'records', header: t('channelOps.errorRecords') }
])
const memberColumns = computed<TableColumn<ops.Member>[]>(() => [
  { id: 'account', header: t('channelOps.account') }, { id: 'state', header: t('channelOps.state') }, { id: 'upstream', header: t('channelOps.upstream') },
  { id: 'actions', header: () => h('span', { class: 'sr-only' }, t('channelOps.actions')) }
])
let request = 0, alive = true, controller: AbortController | undefined
function positiveId(value: unknown) { const n = Number(value); return Number.isSafeInteger(n) && n > 0 ? n : 0 }
function localDate(date: Date) { return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16) }
function date(value: string | null) { return value ? new Date(value).toLocaleString() : '—' }
function ms(value: number | null) { return value == null ? '—' : Math.round(value).toLocaleString() }
function message(e: unknown) { return (e as { status?: number } | null)?.status === 409 ? t('channelOps.conflict') : t('channelOps.failed') }
function upstreamLabel(id: number) { return id <= 0 ? t('channelOps.unassigned') : catalog.value?.upstreams.find(u => u.id === id)?.name || `#${id}` }
async function refresh() {
  const current = ++request; controller?.abort(); controller = new AbortController(); const signal = controller.signal
  loading.value = true; error.value = channelsError.value = catalogError.value = ''
  if (!channelId.value) upstreamId.value = 0
  const start = new Date(from.value), end = new Date(until.value)
  if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()) || end <= start || end.getTime() - start.getTime() > 31 * 86400_000) { error.value = t('channelOps.invalidWindow'); loading.value = false; return }
  const requestedChannel = channelId.value
  const results = await Promise.allSettled([
    channelsAPI.list(channelPage.value, pageSize, { search: channelSearch.value }, { signal }),
    channelId.value ? channelsAPI.getById(channelId.value) : Promise.resolve(null),
    channelId.value ? ops.catalog(channelId.value, { page: memberPage.value, page_size: pageSize, search: memberSearch.value, upstream_page: upstreamPage.value, upstream_search: upstreamSearch.value, upstream_id: 0, membership: membership.value }, signal) : Promise.resolve(null),
    ops.snapshot({ channel_id: channelId.value, upstream_id: upstreamId.value, account_id: accountId.value, group_id: groupId.value || 0, from: start.toISOString(), until: end.toISOString(), model: model.value, model_scope: modelScope.value, dimension: dimension.value, page: statsPage.value, page_size: pageSize, quality_before: qualityBefore.value }, signal)
  ])
  if (!alive || request !== current) return
  const [list, selected, members, stats] = results
  if (list.status === 'fulfilled') { channels.value = list.value.items; channelTotal.value = list.value.total } else channelsError.value = message(list.reason)
  if (selected.status === 'fulfilled') selectedChannel.value = selected.value; else catalogError.value = message(selected.reason)
  if (members.status === 'fulfilled') { catalog.value = members.value; catalogChannelId.value = requestedChannel; catalogStale.value = false } else { catalogError.value = message(members.reason); catalogStale.value = true }
  if (stats.status === 'fulfilled') {
    const next = keepSuccessfulSections(snapshot.value, stats.value)
    snapshot.value = next
    snapshotStale.value = (['usage', 'quality', 'errors', 'capacity'] as const).some(key => !!next[key].stale_error)
  } else { error.value = message(stats.reason); snapshotStale.value = !!snapshot.value }
  loading.value = false
  void router.replace({ query: { ...route.query, channel_id: channelId.value || undefined, upstream_id: upstreamId.value || undefined, account_id: accountId.value || undefined } })
}
async function selectChannel(id: number) {
  if (id !== channelId.value) groupId.value = 0
  channelId.value = id; upstreamId.value = accountId.value = 0
  memberPage.value = upstreamPage.value = statsPage.value = 1
  qualityBefore.value = 0; accountMode.value = false; selectedChannel.value = null
  await refresh()
}
function drill(id: number) { if (!channelId.value) { void selectChannel(id); return } if (dimension.value === 'upstream') upstreamId.value = id || -1; else accountId.value = id; statsPage.value = 1; qualityBefore.value = 0; void refresh() }
function applyFilters() { statsPage.value = 1; qualityBefore.value = 0; void refresh() }
function olderQuality() { const rows = snapshot.value?.quality.data?.rows; if (!rows?.length) return; qualityBefore.value = rows[rows.length - 1].id; void refresh() }
function editChannel(channel: Channel | null) { dialogError.value = ''; channelEdit.value = { id: channel?.id || 0, name: channel?.name || '', description: channel?.description || '', status: channel?.status || 'active' } }
async function saveChannel() {
  if (!channelEdit.value || busy.value) return; const draft = { ...channelEdit.value }; busy.value = true; dialogError.value = ''; notice.value = ''
  try { const body = { name: draft.name.trim(), description: draft.description }; const channel = draft.id ? await channelsAPI.update(draft.id, { ...body, status: draft.status }) : await channelsAPI.create(body); if (!channel || !positiveId(channel.id)) throw new Error('Invalid channel response'); channelEdit.value = null; if (channel.id !== channelId.value) await selectChannel(channel.id); else await refresh(); notice.value = t('channelOps.saved') } catch (e) { dialogError.value = message(e) } finally { busy.value = false }
}
function queueMutation(body: Omit<ops.Mutation, 'revision'>) { if (!catalog.value || catalogStale.value || catalogChannelId.value !== channelId.value) return; error.value = ''; pending.value = { kind: 'mutation', body: { ...body, revision: catalog.value.revision }, channelId: channelId.value } }
async function performMutation(body: ops.Mutation, id = channelId.value) {
  if (busy.value || catalogStale.value || catalogChannelId.value !== id) return; busy.value = true; dialogError.value = ''; error.value = ''; notice.value = ''
  try { await ops.mutate(id, body); pending.value = null; upstreamEdit.value = null; assigning.value = null; notice.value = t('channelOps.saved'); await refresh() } catch (e) { const failure = message(e); pending.value = null; await refresh(); dialogError.value = error.value = failure } finally { busy.value = false }
}
async function saveUpstream() { if (!upstreamEdit.value || !catalog.value) return; await performMutation({ action: upstreamEdit.value.id ? 'update_upstream' : 'create_upstream', upstream_id: upstreamEdit.value.id, name: upstreamEdit.value.name.trim(), revision: catalog.value.revision }) }
async function assignMember() { if (!assigning.value || !catalog.value) return; const body: ops.Mutation = { action: 'assign', revision: catalog.value.revision, upstream_id: targetUpstream.value, account_id: assigning.value.id, expected_upstream_id: assigning.value.upstream_id, confirm: !!assigning.value.upstream_id }; if (assigning.value.upstream_id) queueMutation(body); else await performMutation(body) }
async function confirmPending() {
  const action = pending.value; if (!action || busy.value) return
  if (action.kind === 'mutation') { await performMutation(action.body, action.channelId); return }
  busy.value = true; error.value = ''; notice.value = ''
  try { await channelsAPI.remove(action.channel.id); pending.value = null; await selectChannel(0); notice.value = t('channelOps.saved') } catch (e) { pending.value = null; error.value = message(e) } finally { busy.value = false }
}
onMounted(refresh)
onBeforeUnmount(() => { alive = false; request++; controller?.abort() })
</script>
