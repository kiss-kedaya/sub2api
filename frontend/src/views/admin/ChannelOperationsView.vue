<template>
  <AppLayout>
    <div class="mx-auto min-w-0 max-w-7xl space-y-5">
      <AdminPageHeader :title="t('channelOps.title')" :description="t('channelOps.description')">
        <template #actions><UButton icon="i-lucide-refresh-cw" color="neutral" variant="outline" :disabled="loading || busy" :label="t('channelOps.refresh')" @click="refresh" /></template>
      </AdminPageHeader>
      <UCollapsible :unmount-on-hide="false" class="rounded-lg bg-muted/50 px-3 py-2">
        <UButton color="neutral" variant="link" size="sm" icon="i-lucide-info" trailing-icon="i-lucide-chevron-down" :label="t('channelOps.scopeTitle')" />
        <template #content><p class="px-2 pb-2 pt-3 text-sm leading-6 text-muted">{{ t('channelOps.scope') }}</p></template>
      </UCollapsible>
      <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
      <UAlert v-if="notice" role="status" color="success" variant="subtle" icon="i-lucide-circle-check" :title="notice" />
      <UAlert v-if="loading" role="status" color="neutral" variant="subtle" icon="i-lucide-loader-circle" :title="t('channelOps.loading')" />
      <UAlert v-if="snapshotStale" role="status" color="warning" variant="subtle" icon="i-lucide-history" :title="t('channelOps.previousData')" />
      <fieldset :disabled="busy || loading" class="min-w-0 space-y-5">
        <UPageCard :title="t('channelOps.channels')" variant="outline" :ui="cardUi">
          <div class="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between">
            <form class="flex w-full min-w-0 flex-wrap items-center gap-2 sm:w-auto sm:flex-1" @submit.prevent="channelPage = 1; refresh()">
              <UInput v-model="channelSearch" icon="i-lucide-search" class="w-full sm:w-64" maxlength="100" :placeholder="t('channelOps.search')" :aria-label="t('channelOps.search')" />
              <UButton type="submit" color="neutral" variant="outline" :label="t('channelOps.apply')" />
              <UButton color="neutral" :variant="!channelId ? 'soft' : 'ghost'" icon="i-lucide-layers" :label="t('channelOps.overview')" @click="selectChannel(0)" />
            </form>
            <div class="flex flex-wrap gap-2">
              <UButton data-testid="new-channel" icon="i-lucide-plus" :label="t('channelOps.createChannel')" @click="editChannel(null)" />
              <UButton to="/admin/channels/pricing" color="neutral" variant="outline" :label="t('channelOps.pricing')" />
            </div>
          </div>
          <UAlert v-if="channelsError" role="alert" color="error" variant="subtle" :title="channelsError" />
          <div v-if="channels.length" class="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
            <UButton v-for="channel in channels" :key="channel.id" color="neutral" :variant="channelId === channel.id ? 'soft' : 'outline'" class="w-full justify-between px-3 py-3 text-left" :class="channelId === channel.id ? 'ring-2 ring-primary' : ''" :aria-pressed="channelId === channel.id" @click="selectChannel(channel.id)">
              <span class="flex min-w-0 items-center gap-2"><UIcon :name="channelId === channel.id ? 'i-lucide-circle-check' : 'i-lucide-radio'" class="size-4 shrink-0" /><span class="truncate font-medium">{{ channel.name }}</span></span>
              <UBadge :color="channel.status === 'active' ? 'success' : 'neutral'" variant="subtle" size="sm">{{ t(channel.status === 'active' ? 'channelOps.channelActive' : 'channelOps.channelDisabled') }}</UBadge>
            </UButton>
          </div>
          <div v-else-if="!channelsError" class="rounded-lg border border-dashed border-default px-4 py-8 text-center text-sm text-muted">{{ loading ? t('channelOps.loading') : t('channelOps.empty') }}</div>
          <div class="flex flex-wrap items-center justify-between gap-3 border-t border-default pt-3">
            <span class="text-sm tabular-nums text-muted">{{ t('channelOps.pageSummary', { page: channelPage, total: channelTotal }) }}</span>
            <div class="flex gap-2"><UButton color="neutral" variant="outline" size="sm" :disabled="channelPage <= 1" :label="t('channelOps.previous')" @click="channelPage--; refresh()" /><UButton color="neutral" variant="outline" size="sm" :disabled="channelPage * pageSize >= channelTotal" :label="t('channelOps.next')" @click="channelPage++; refresh()" /></div>
          </div>
          <div v-if="selectedChannel && selectedChannel.id === channelId" class="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-muted/50 p-3">
            <div class="min-w-0"><strong class="break-words text-sm">{{ selectedChannel.name }} <span class="font-normal text-muted">#{{ channelId }}</span></strong><p class="mt-1 text-xs text-muted">{{ t('channelOps.groupLabel') }}: {{ selectedChannel.group_ids?.join(', ') || '—' }}</p></div>
            <div class="flex flex-wrap gap-2 sm:ml-auto">
              <UButton data-testid="edit-channel" color="neutral" variant="outline" size="sm" icon="i-lucide-pencil" :label="t('channelOps.edit')" @click="editChannel(selectedChannel)" />
              <UButton color="neutral" variant="outline" size="sm" :to="{ path: '/admin/channels/pricing', query: { edit_channel: channelId } }" :label="t('channelOps.fullConfig')" />
              <UButton data-testid="delete-channel" color="error" variant="subtle" size="sm" :label="t('channelOps.deleteChannel')" @click="pending = { kind: 'channel', channel: selectedChannel }" />
            </div>
          </div>
        </UPageCard>
        <UCollapsible :unmount-on-hide="false" class="rounded-lg border border-default bg-default p-4">
          <UButton color="neutral" variant="link" icon="i-lucide-sliders-horizontal" trailing-icon="i-lucide-chevron-down" :label="t('channelOps.advancedFilters')" />
          <template #content>
            <form class="mt-4 grid min-w-0 gap-4 sm:grid-cols-2 lg:grid-cols-3" @submit.prevent="applyFilters">
              <UFormField :label="t('channelOps.from')"><UInput v-model="from" class="w-full min-w-0" type="datetime-local" required /></UFormField>
              <UFormField :label="t('channelOps.until')"><UInput v-model="until" class="w-full min-w-0" type="datetime-local" required /></UFormField>
              <UFormField :label="t('channelOps.groups')"><UInput v-model.number="groupId" class="w-full" type="number" min="0" step="1" /></UFormField>
              <UFormField :label="t('channelOps.modelScope')"><USelect v-model="modelScope" class="w-full" :items="[{ value: 'public', label: t('channelOps.public') }, { value: 'recorded', label: t('channelOps.recorded') }, { value: 'upstream', label: t('channelOps.upstreamModel') }]" /></UFormField>
              <UFormField :label="t('channelOps.model')"><UInput v-model="model" class="w-full" maxlength="200" /></UFormField>
              <div class="flex items-end justify-end"><UButton type="submit" icon="i-lucide-check" :label="t('channelOps.apply')" /></div>
            </form>
          </template>
        </UCollapsible>
        <nav :aria-label="t('channelOps.drilldown')">
          <ol class="flex flex-wrap items-center gap-1 text-sm">
            <li><UButton color="neutral" variant="link" size="sm" icon="i-lucide-layers" :label="t('channelOps.overview')" @click="selectChannel(0)" /></li>
            <li v-if="channelId" class="flex items-center gap-1"><UIcon name="i-lucide-chevron-right" class="size-4 text-dimmed" /><UButton color="neutral" variant="link" size="sm" :label="selectedChannel?.name || `#${channelId}`" @click="selectChannel(channelId)" /></li>
            <li v-if="upstreamId" class="flex items-center gap-1"><UIcon name="i-lucide-chevron-right" class="size-4 text-dimmed" /><UButton color="neutral" variant="link" size="sm" :label="upstreamLabel(upstreamId)" @click="accountId = 0; statsPage = 1; qualityBefore = 0; refresh()" /></li>
            <li v-if="accountId" aria-current="page" class="flex items-center gap-2 font-medium"><UIcon name="i-lucide-chevron-right" class="size-4 text-dimmed" />{{ t('channelOps.account') }} #{{ accountId }}</li>
          </ol>
        </nav>
        <div v-if="snapshot" data-testid="applied-scope" class="space-y-2">
          <p class="text-xs font-medium text-muted">{{ t('channelOps.appliedScope') }}</p>
          <div class="flex flex-wrap gap-2">
            <UBadge color="neutral" variant="subtle" class="max-w-full whitespace-normal break-words" icon="i-lucide-calendar-days">{{ date(snapshot.from) }} → {{ date(snapshot.until) }}</UBadge>
            <UBadge color="neutral" variant="subtle">{{ t('channelOps.channels') }}: {{ scopeId(snapshot.scope.channel_id) }}</UBadge>
            <UBadge color="neutral" variant="subtle">{{ t('channelOps.upstream') }}: {{ snapshot.scope.upstream_id === -1 ? t('channelOps.unassigned') : scopeId(snapshot.scope.upstream_id) }}</UBadge>
            <UBadge color="neutral" variant="subtle">{{ t('channelOps.account') }}: {{ scopeId(snapshot.scope.account_id) }}</UBadge>
            <UBadge color="neutral" variant="subtle">{{ t('channelOps.groupLabel') }}: {{ scopeId(snapshot.scope.group_id) }}</UBadge>
            <UBadge color="neutral" variant="subtle" class="max-w-full whitespace-normal break-all">{{ t('channelOps.modelScope') }}: {{ t(snapshot.scope.model_scope === 'recorded' ? 'channelOps.recorded' : snapshot.scope.model_scope === 'upstream' ? 'channelOps.upstreamModel' : 'channelOps.public') }} · {{ snapshot.scope.model || t('channelOps.all') }}</UBadge>
          </div>
        </div>
        <UPageCard v-if="snapshot" data-testid="usage-section" :title="t('channelOps.latency')" variant="outline" :ui="cardUi">
          <p class="text-sm leading-6 text-muted">{{ t('channelOps.latencyNote') }}</p>
          <UAlert v-if="snapshot.usage.status !== 'ready'" role="alert" color="error" variant="subtle" :title="snapshot.usage.error" />
          <template v-else-if="snapshot.usage.data">
            <ChannelOperationsMetrics :metrics="snapshot.usage.data.summary" />
            <p class="text-xs leading-5 text-muted">{{ t('channelOps.queried') }} {{ date(snapshot.usage.queried_at) }} · {{ t('channelOps.latest') }} {{ date(snapshot.usage.data.summary.latest_sample_at) }}</p>
            <p v-if="!snapshot.usage.data.summary.requests" data-testid="no-usage" class="text-sm text-muted">{{ t('channelOps.noUsage') }}</p>
            <UTable :data="snapshot.usage.data.rows" :loading="loading" :columns="[{ accessorKey: 'name', header: t('channelOps.dimension') }, { accessorKey: 'requests', header: t('channelOps.requests') }, { id: 'samples', header: `${t('channelOps.known')} / ${t('channelOps.unknown')}` }, { id: 'latency', header: t('channelOps.latencyPercentiles') }]" :ui="tableUi">
              <template #name-cell="{ row }"><UButton v-if="!accountId && (row.original.dimension_id || dimension === 'upstream')" color="neutral" variant="link" class="px-0" :label="row.original.name" @click="drill(row.original.dimension_id)" /><span v-else>{{ row.original.name }}</span></template>
              <template #samples-cell="{ row }">{{ row.original.samples }} / {{ row.original.unknown_samples }}</template>
              <template #latency-cell="{ row }">{{ ms(row.original.p50_ms) }} / {{ ms(row.original.p90_ms) }} / {{ ms(row.original.p95_ms) }}</template>
              <template #empty><div class="sticky left-0 w-[calc(100vw-4rem)] max-w-full whitespace-normal px-4 py-6 text-center sm:w-full">{{ t('channelOps.empty') }}</div></template>
            </UTable>
            <div class="flex flex-wrap items-center justify-between gap-3">
              <span class="text-sm tabular-nums text-muted">{{ t('channelOps.pageSummary', { page: statsPage, total: snapshot.usage.data.total }) }}</span>
              <div class="flex flex-wrap gap-2"><UButton v-if="channelId && !accountId && !upstreamId" color="neutral" variant="soft" size="sm" :label="t(accountMode ? 'channelOps.byUpstream' : 'channelOps.byAccount')" @click="accountMode = !accountMode; statsPage = 1; refresh()" /><UButton color="neutral" variant="outline" size="sm" :disabled="statsPage <= 1" :label="t('channelOps.previous')" @click="statsPage--; refresh()" /><UButton data-testid="usage-next" color="neutral" variant="outline" size="sm" :disabled="statsPage * pageSize >= snapshot.usage.data.total" :label="t('channelOps.next')" @click="statsPage++; refresh()" /></div>
            </div>
            <UCollapsible :unmount-on-hide="false" class="border-t border-default pt-3">
              <UButton color="neutral" variant="link" size="sm" trailing-icon="i-lucide-chevron-down" :label="`${t('channelOps.lineage')} (${snapshot.usage.data.lineage.length} / ${snapshot.usage.data.lineage_total})`" />
              <template #content>
                <p class="my-3 text-xs leading-5 text-muted">{{ t('channelOps.lineageNote') }}</p>
                <UTable :data="snapshot.usage.data.lineage" :loading="loading" :columns="[{ accessorKey: 'public_model', header: t('channelOps.public') }, { accessorKey: 'recorded_model', header: t('channelOps.recorded') }, { accessorKey: 'upstream_model', header: t('channelOps.upstreamModel') }, { accessorKey: 'model_mapping_chain', header: t('channelOps.chain') }, { accessorKey: 'samples', header: t('channelOps.requests') }]" :ui="tableUi"><template #upstream_model-cell="{ row }">{{ row.original.upstream_model || '—' }}</template><template #model_mapping_chain-cell="{ row }">{{ row.original.model_mapping_chain || '—' }}</template><template #empty><div class="sticky left-0 w-[calc(100vw-4rem)] max-w-full whitespace-normal px-4 py-6 text-center sm:w-full">{{ t('channelOps.empty') }}</div></template></UTable>
              </template>
            </UCollapsible>
          </template>
          <div class="flex flex-wrap items-center gap-x-2 text-sm text-muted"><span>{{ t('channelOps.errorNote') }}</span><UButton to="/admin/ops" color="neutral" variant="link" size="sm" class="px-0" :label="t('channelOps.errors')" /></div>
        </UPageCard>
        <UPageCard v-if="snapshot" data-testid="quality-section" :title="t('channelOps.quality')" variant="outline" :ui="cardUi">
          <p class="text-sm leading-6 text-muted">{{ t('channelOps.qualityNote') }}</p>
          <UAlert v-if="snapshot.quality.status !== 'ready'" role="alert" color="error" variant="subtle" :title="snapshot.quality.error" />
          <template v-else-if="snapshot.quality.data">
            <div class="flex flex-wrap gap-2"><UBadge color="neutral" variant="subtle">{{ t('channelOps.rounds') }} {{ snapshot.quality.data.summary.rounds }}</UBadge><UBadge color="success" variant="subtle" icon="i-lucide-circle-check">{{ t('channelOps.passed') }} {{ snapshot.quality.data.summary.passed }}</UBadge><UBadge color="error" variant="subtle" icon="i-lucide-circle-x">{{ t('channelOps.degraded') }} {{ snapshot.quality.data.summary.degraded }}</UBadge><UBadge color="neutral" variant="subtle" icon="i-lucide-circle-help">{{ t('channelOps.inconclusive') }} {{ snapshot.quality.data.summary.inconclusive }}</UBadge></div>
            <p class="text-xs leading-5 text-muted">{{ t('channelOps.queried') }} {{ date(snapshot.quality.queried_at) }} · {{ t('channelOps.latest') }} {{ date(snapshot.quality.data.summary.latest_sample_at) }}</p>
            <UTable :data="snapshot.quality.data.rows" :loading="loading" :columns="[{ accessorKey: 'observed_at', header: t('channelOps.time') }, { accessorKey: 'account_id', header: t('channelOps.account') }, { accessorKey: 'measured_model', header: t('channelOps.modelLabel') }, { accessorKey: 'outcome', header: t('channelOps.outcome') }, { id: 'probes', header: t('channelOps.probes') }, { id: 'actions', header: t('channelOps.actions') }, { accessorKey: 'plan_id', header: t('channelOps.plan') }]" :ui="tableUi">
              <template #observed_at-cell="{ row }">{{ date(row.original.observed_at) }}</template>
              <template #account_id-cell="{ row }"><UButton color="neutral" variant="link" size="sm" class="px-0" :label="`#${row.original.account_id}`" @click="accountId = row.original.account_id; statsPage = 1; qualityBefore = 0; refresh()" /></template>
              <template #measured_model-cell="{ row }">{{ row.original.measured_model || '—' }}</template>
              <template #outcome-cell="{ row }"><UBadge :color="row.original.outcome === 'degraded' ? 'error' : row.original.outcome === 'passed' ? 'success' : 'neutral'" variant="subtle" :icon="row.original.outcome === 'degraded' ? 'i-lucide-circle-x' : row.original.outcome === 'passed' ? 'i-lucide-circle-check' : 'i-lucide-circle-help'">{{ t(`channelOps.${row.original.outcome}`) }}</UBadge><p v-if="!row.original.round_complete" class="mt-1 whitespace-normal text-xs text-warning">{{ t('channelOps.incompleteRound') }} {{ row.original.round_probes }} / {{ row.original.round_expected ?? '—' }}</p></template>
              <template #probes-cell="{ row }">{{ row.original.passed_probes }} / {{ row.original.probes }} ({{ row.original.expected ?? '—' }})</template>
              <template #actions-cell="{ row }"><UCollapsible v-if="row.original.actions.some(Boolean)" :unmount-on-hide="false"><UButton color="neutral" variant="link" size="sm" class="px-0" trailing-icon="i-lucide-chevron-down" :label="t('channelOps.actionDetails')" /><template #content><p class="max-w-64 whitespace-normal break-words text-xs text-muted">{{ row.original.actions.filter(Boolean).join(', ') }}</p></template></UCollapsible><span v-else>—</span></template>
              <template #plan_id-cell="{ row }">#{{ row.original.plan_id }}</template>
              <template #empty><div class="sticky left-0 w-[calc(100vw-4rem)] max-w-full whitespace-normal px-4 py-6 text-center sm:w-full">{{ t('channelOps.noQuality') }}</div></template>
            </UTable>
            <div class="flex justify-end gap-2"><UButton color="neutral" variant="outline" size="sm" :disabled="!qualityBefore" :label="t('channelOps.latestPage')" @click="qualityBefore = 0; refresh()" /><UButton color="neutral" variant="outline" size="sm" :disabled="!snapshot.quality.data.has_more" :label="t('channelOps.older')" @click="olderQuality" /></div>
          </template>
        </UPageCard>
        <UPageCard v-if="snapshot" data-testid="errors-section" :title="t('channelOps.errors')" variant="outline" :ui="cardUi">
          <p class="text-sm leading-6 text-muted">{{ t('channelOps.errorsScope') }}</p>
          <UAlert v-if="snapshot.errors.status !== 'ready'" role="alert" color="error" variant="subtle" :title="snapshot.errors.error" />
          <template v-else-if="snapshot.errors.data">
            <div class="flex flex-wrap gap-2"><UBadge color="neutral" variant="subtle">{{ t('channelOps.errorRecords') }} {{ snapshot.errors.data.records }}</UBadge><UBadge color="warning" variant="subtle">{{ t('channelOps.upstream5xx') }} {{ snapshot.errors.data.upstream_5xx }}</UBadge></div>
            <p class="text-xs leading-5 text-muted">{{ t('channelOps.errorSource') }} · {{ t('channelOps.queried') }} {{ date(snapshot.errors.queried_at) }} · {{ t('channelOps.latest') }} {{ date(snapshot.errors.data.latest_sample_at) }}</p>
            <UTable :data="snapshot.errors.data.rows" :loading="loading" :columns="[{ accessorKey: 'error_type', header: t('channelOps.errorType') }, { accessorKey: 'error_phase', header: t('channelOps.errorPhase') }, { accessorKey: 'status_code', header: t('channelOps.httpStatus') }, { accessorKey: 'records', header: t('channelOps.errorRecords') }]" :ui="tableUi">
              <template #error_type-cell="{ row }">{{ diagnosticLabel(row.original.error_type) }}</template><template #error_phase-cell="{ row }">{{ diagnosticLabel(row.original.error_phase) }}</template><template #status_code-cell="{ row }"><UBadge color="neutral" variant="subtle">{{ row.original.status_code || '—' }}</UBadge></template><template #empty><div class="sticky left-0 w-[calc(100vw-4rem)] max-w-full whitespace-normal px-4 py-6 text-center sm:w-full">{{ t('channelOps.noErrors') }}</div></template>
            </UTable>
            <p class="text-xs text-muted">{{ snapshot.errors.data.rows.length }} / {{ snapshot.errors.data.bucket_total }} · {{ t('channelOps.topErrors') }}</p>
          </template>
        </UPageCard>
        <UPageCard v-if="snapshot" :title="t('channelOps.capacity')" variant="outline" :ui="cardUi">
          <p class="text-sm leading-6 text-muted">{{ t('channelOps.capacityNote') }}</p>
          <UAlert v-if="snapshot.capacity.status !== 'ready'" role="alert" color="error" variant="subtle" :title="snapshot.capacity.error" />
          <template v-else-if="snapshot.capacity.data">
            <UTable :data="[snapshot.capacity.data]" :loading="loading" :columns="[{ accessorKey: 'accounts', header: t('channelOps.accounts') }, { accessorKey: 'schedulable', header: t('channelOps.schedulable') }, { accessorKey: 'configured_concurrency', header: t('channelOps.concurrency') }]" :ui="tableUi"><template #empty><div class="sticky left-0 w-[calc(100vw-4rem)] max-w-full whitespace-normal px-4 py-6 text-center sm:w-full">{{ t('channelOps.empty') }}</div></template></UTable>
            <p class="text-xs text-muted">{{ t('channelOps.queried') }} {{ date(snapshot.capacity.queried_at) }}</p>
          </template>
        </UPageCard>
        <UPageCard v-if="channelId" :title="t('channelOps.membership')" variant="outline" :ui="cardUi">
          <p class="text-sm leading-6 text-muted">{{ t('channelOps.membershipNote') }}</p>
          <UAlert v-if="catalogError" role="alert" color="error" variant="subtle" :title="catalogError" />
          <fieldset v-if="catalog" :disabled="catalogStale || catalogChannelId !== channelId" class="min-w-0 space-y-4">
            <form class="flex flex-wrap items-center gap-2" @submit.prevent="upstreamPage = 1; refresh()"><UInput v-model="upstreamSearch" icon="i-lucide-search" class="w-full sm:w-64" maxlength="100" :placeholder="t('channelOps.searchUpstream')" :aria-label="t('channelOps.searchUpstream')" /><UButton type="submit" color="neutral" variant="outline" :label="t('channelOps.apply')" /><UButton icon="i-lucide-plus" :label="t('channelOps.createUpstream')" @click="dialogError = ''; upstreamEdit = { id: 0, name: '' }" /></form>
            <UTable :data="catalog.upstreams" :loading="loading" :columns="[{ accessorKey: 'name', header: t('channelOps.upstream') }, { id: 'actions', header: t('channelOps.actions') }]" :ui="tableUi">
              <template #name-cell="{ row }"><UButton color="neutral" variant="link" class="px-0" :label="`${row.original.name} (#${row.original.id}) · ${row.original.members}`" @click="upstreamId = row.original.id; accountId = 0; statsPage = 1; qualityBefore = 0; refresh()" /></template>
              <template #actions-cell="{ row }"><div class="flex justify-end gap-2"><UButton color="neutral" variant="ghost" size="sm" :label="t('channelOps.rename')" @click="dialogError = ''; upstreamEdit = { id: row.original.id, name: row.original.name }" /><UButton color="error" variant="ghost" size="sm" :label="t('channelOps.deleteUpstream')" @click="queueMutation({ action: 'delete_upstream', upstream_id: row.original.id, confirm: true })" /></div></template>
              <template #empty><div class="sticky left-0 w-[calc(100vw-4rem)] max-w-full whitespace-normal px-4 py-6 text-center sm:w-full">{{ t('channelOps.noUpstream') }}</div></template>
            </UTable>
            <div class="flex flex-wrap items-center justify-between gap-3"><span class="text-sm tabular-nums text-muted">{{ t('channelOps.pageSummary', { page: upstreamPage, total: catalog.upstream_total }) }}</span><div class="flex gap-2"><UButton color="neutral" variant="outline" size="sm" :disabled="upstreamPage <= 1" :label="t('channelOps.previous')" @click="upstreamPage--; refresh()" /><UButton data-testid="upstream-next" color="neutral" variant="outline" size="sm" :disabled="upstreamPage * pageSize >= catalog.upstream_total" :label="t('channelOps.next')" @click="upstreamPage++; refresh()" /></div></div>
            <form class="flex flex-wrap items-end gap-3 border-t border-default pt-4" @submit.prevent="memberPage = 1; refresh()"><UFormField :label="t('channelOps.searchAccount')" class="w-full sm:w-64"><UInput v-model="memberSearch" icon="i-lucide-search" maxlength="100" class="w-full" /></UFormField><UFormField :label="t('channelOps.members')" class="w-full sm:w-40"><USelect v-model="membership" class="w-full" :items="[{ value: 'all', label: t('channelOps.all') }, { value: 'assigned', label: t('channelOps.assigned') }, { value: 'unassigned', label: t('channelOps.unassigned') }]" /></UFormField><UButton type="submit" color="neutral" variant="outline" :label="t('channelOps.apply')" /></form>
            <UTable :data="catalog.accounts" :loading="loading" :columns="[{ accessorKey: 'name', header: t('channelOps.account') }, { id: 'state', header: t('channelOps.state') }, { accessorKey: 'upstream_id', header: t('channelOps.upstream') }, { id: 'actions', header: t('channelOps.actions') }]" :ui="tableUi">
              <template #name-cell="{ row }"><UButton color="neutral" variant="link" class="px-0" :label="`${row.original.name} #${row.original.id}`" @click="accountId = row.original.id; upstreamId = 0; statsPage = 1; qualityBefore = 0; refresh()" /><p class="text-xs text-muted">{{ row.original.platform }}</p></template>
              <template #state-cell="{ row }"><div class="flex items-center gap-2"><UBadge :color="row.original.status === 'active' ? 'success' : 'neutral'" variant="subtle">{{ t(row.original.status === 'active' ? 'channelOps.channelActive' : row.original.status === 'disabled' ? 'channelOps.channelDisabled' : row.original.status === 'error' ? 'channelOps.accountError' : 'channelOps.otherState') }}</UBadge><span class="tabular-nums">{{ row.original.concurrency }}</span></div><p v-if="!row.original.eligible" class="mt-1 text-xs text-warning">{{ t('channelOps.ineligible') }}</p></template>
              <template #upstream_id-cell="{ row }">{{ upstreamLabel(row.original.upstream_id) }}</template>
              <template #actions-cell="{ row }"><div class="flex gap-2"><UButton color="neutral" variant="outline" size="sm" :disabled="!row.original.eligible" :label="t('channelOps.assign')" @click="dialogError = ''; assigning = row.original; targetUpstream = 0" /><UButton v-if="row.original.upstream_id" color="error" variant="subtle" size="sm" :label="t('channelOps.detach')" @click="queueMutation({ action: 'detach', account_id: row.original.id, expected_upstream_id: row.original.upstream_id, confirm: true })" /></div></template>
              <template #empty><div class="sticky left-0 w-[calc(100vw-4rem)] max-w-full whitespace-normal px-4 py-6 text-center sm:w-full">{{ t('channelOps.empty') }}</div></template>
            </UTable>
            <div class="flex flex-wrap items-center justify-between gap-3"><span class="text-sm tabular-nums text-muted">{{ t('channelOps.pageSummary', { page: memberPage, total: catalog.total }) }}</span><div class="flex gap-2"><UButton color="neutral" variant="outline" size="sm" :disabled="memberPage <= 1" :label="t('channelOps.previous')" @click="memberPage--; refresh()" /><UButton data-testid="member-next" color="neutral" variant="outline" size="sm" :disabled="memberPage * pageSize >= catalog.total" :label="t('channelOps.next')" @click="memberPage++; refresh()" /></div></div>
          </fieldset>
        </UPageCard>
      </fieldset>
      <BaseDialog :show="!!channelEdit" :title="t(channelEdit?.id ? 'channelOps.edit' : 'channelOps.createChannel')" @close="!busy && (channelEdit = null)">
        <form v-if="channelEdit" class="space-y-4" @submit.prevent="saveChannel"><fieldset :disabled="busy" class="space-y-4">
          <UFormField :label="t('channelOps.name')" required><UInput v-model="channelEdit.name" class="w-full" required maxlength="100" /></UFormField>
          <UFormField :label="t('channelOps.descriptionField')"><UTextarea v-model="channelEdit.description" class="w-full" maxlength="2000" :rows="3" /></UFormField>
          <UFormField v-if="channelEdit.id" :label="t('channelOps.channelStatus')"><USelect v-model="channelEdit.status" class="w-full" :items="[{ value: 'active', label: t('channelOps.channelActive') }, { value: 'disabled', label: t('channelOps.channelDisabled') }]" /></UFormField>
          <p class="text-sm leading-6 text-muted">{{ t('channelOps.basicEdit') }}</p><UAlert v-if="dialogError" role="alert" color="error" variant="subtle" :title="dialogError" />
          <div class="flex justify-end"><UButton type="submit" :loading="busy" :label="t('channelOps.save')" /></div>
        </fieldset></form>
      </BaseDialog>
      <BaseDialog :show="!!upstreamEdit" :title="t('channelOps.upstream')" @close="!busy && (upstreamEdit = null)">
        <form v-if="upstreamEdit" class="space-y-4" @submit.prevent="saveUpstream"><UFormField :label="t('channelOps.name')" required><UInput v-model="upstreamEdit.name" class="w-full" required maxlength="100" :disabled="busy" /></UFormField><UAlert v-if="dialogError" role="alert" color="error" variant="subtle" :title="dialogError" /><div class="flex justify-end"><UButton type="submit" :disabled="busy" :loading="busy" :label="t('channelOps.save')" /></div></form>
      </BaseDialog>
      <BaseDialog :show="!!assigning" :title="t('channelOps.assign')" @close="!busy && (assigning = null)">
        <div class="space-y-4">
          <div class="rounded-lg bg-muted/50 p-3 text-sm font-medium">{{ assigning?.name }} <span class="text-muted">#{{ assigning?.id }}</span></div><p class="text-sm leading-6 text-muted">{{ t('channelOps.targetHint') }}</p>
          <form class="flex flex-wrap gap-2" @submit.prevent="upstreamPage = 1; refresh()"><UInput v-model="upstreamSearch" icon="i-lucide-search" class="min-w-0 flex-1" maxlength="100" :placeholder="t('channelOps.searchUpstream')" :aria-label="t('channelOps.searchUpstream')" /><UButton type="submit" color="neutral" variant="outline" :disabled="loading" :label="t('channelOps.apply')" /></form>
          <div v-if="catalog" class="space-y-3">
            <UFormField :label="t('channelOps.upstream')"><div class="max-h-56 space-y-2 overflow-y-auto"><label v-for="target in catalog.upstreams" :key="target.id" class="flex cursor-pointer items-center gap-3 rounded-lg border border-default p-3 text-sm has-[:checked]:bg-muted"><input v-model.number="targetUpstream" type="radio" name="target-upstream" :value="target.id" class="size-4 accent-primary" /><span>{{ target.name }} <span class="text-muted">#{{ target.id }}</span></span></label><p v-if="!catalog.upstreams.length" class="py-4 text-sm text-muted">{{ t('channelOps.noUpstream') }}</p></div></UFormField>
            <div class="flex flex-wrap items-center justify-between gap-2"><span class="text-xs tabular-nums text-muted">{{ t('channelOps.pageSummary', { page: upstreamPage, total: catalog.upstream_total }) }}</span><div class="flex gap-2"><UButton color="neutral" variant="outline" size="sm" :disabled="loading || upstreamPage <= 1" :label="t('channelOps.previous')" @click="upstreamPage--; refresh()" /><UButton color="neutral" variant="outline" size="sm" :disabled="loading || upstreamPage * pageSize >= catalog.upstream_total" :label="t('channelOps.next')" @click="upstreamPage++; refresh()" /></div></div>
          </div>
          <UFormField :label="t('channelOps.target')"><UInput v-model.number="targetUpstream" class="w-full" type="number" min="1" step="1" :disabled="busy" /></UFormField><p class="text-sm leading-6 text-muted">{{ t('channelOps.reassignNote') }}</p><UAlert v-if="dialogError" role="alert" color="error" variant="subtle" :title="dialogError" />
          <div class="flex justify-end"><UButton :disabled="busy || loading || !catalog || !targetUpstream" :loading="busy" :label="t('channelOps.save')" @click="assignMember" /></div>
        </div>
      </BaseDialog>
      <ConfirmDialog :show="!!pending" :title="t('channelOps.confirmTitle')" :message="confirmationMessage" :confirm-text="t('channelOps.confirm')" :danger="true" @cancel="!busy && (pending = null)" @confirm="confirmPending" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
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

const cardUi = { container: 'min-w-0 gap-y-4 p-4 sm:p-5', wrapper: 'min-w-0', description: 'whitespace-normal break-words' }
const tableUi = { root: 'w-full min-w-0 max-w-full overflow-x-auto', base: 'w-full min-w-[640px]', th: 'bg-muted/50 text-xs font-semibold', td: 'text-sm', empty: 'p-0' }
function scopeId(id: number) { return id ? '#' + id : t('channelOps.all') }
function diagnosticLabel(value: string) {
  const labels: Record<string, string> = { upstream: 'diagnosticUpstream', forward: 'diagnosticForward', request: 'diagnosticRequest', internal: 'diagnosticInternal', auth: 'diagnosticAuth', authentication: 'diagnosticAuth', scheduling: 'diagnosticScheduling', schedule: 'diagnosticScheduling', timeout: 'diagnosticTimeout', rate_limit: 'diagnosticRateLimit' }
  return value ? t('channelOps.' + (labels[value] || 'diagnosticOther')) : '—'
}
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
