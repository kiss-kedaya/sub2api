<template>
  <div class="space-y-5" :aria-busy="busy">
    <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-700">
      <div class="space-y-1">
        <p class="text-sm font-medium" role="status">Mihomo <span class="badge ml-2" :class="status?.running ? 'badge-success' : 'badge-gray'">{{ status?.phase || '—' }}</span></p>
        <p class="text-xs text-gray-500">{{ text('当前服务器', 'Current server') }} <code>{{ status?.host_id || '—' }}</code> · {{ status?.nodes || 0 }} {{ text('个节点', 'nodes') }}</p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="refresh">{{ text('检测状态', 'Check status') }}</button>
    </div>
    <div v-if="warmPools.length" class="grid gap-3 sm:grid-cols-2">
      <div v-for="pool in warmPools" :key="pool.source" class="rounded-xl border border-gray-200 p-3 text-xs dark:border-dark-600" data-testid="bps-warm-pool">
        <p class="font-medium">{{ pool.source }} · Warm IP pool</p>
        <p class="mt-2">{{ text('就绪', 'Ready') }} {{ pool.ready }} / {{ text('目标', 'Target') }} {{ pool.target }} · {{ text('探测中', 'Checking') }} {{ pool.checking }} · {{ text('冷却中', 'Cooling') }} {{ pool.cooling }}</p>
        <p v-if="pool.source === 'Mihomo' && pool.ready_subscription !== undefined" class="mt-1">{{ text('订阅就绪', 'Ready subscription exits') }} {{ pool.ready_subscription }} · {{ text('动态就绪', 'Ready dynamic exits') }} {{ pool.ready_dynamic || 0 }}</p>
        <p class="mt-1 text-gray-500">{{ text('目标按已启用账号的并发数汇总；不足时复用就绪出口，用户请求不探测冷节点。', 'Targets follow enabled account concurrency. Ready exits are reused when scarce; requests never probe cold nodes.') }}</p>
        <p v-for="(count, reason) in pool.failure_reasons" :key="reason">{{ reason }}: {{ count }}</p>
        <p v-if="pool.target > 0 && pool.ready === 0" class="mt-1 text-amber-700">{{ text('后台正在预热；不会退回直连。', 'Background warming continues; direct fallback is disabled.') }}</p>
      </div>
    </div>
    <p v-if="error || status?.error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20">{{ error || status?.error }}</p>
    <p v-if="success" role="status" class="text-sm text-green-700 dark:text-green-400">{{ success }}</p>
    <p v-if="busy" role="status" class="text-sm text-primary-600">{{ text('正在应用，请等待完成…', 'Applying changes. Please wait…') }}</p>

    <template v-if="section === 'subscriptions'">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="font-semibold">{{ text('Mihomo 订阅', 'Mihomo subscriptions') }}</h2><p class="mt-1 text-xs text-gray-500">{{ text('按订阅管理来源；地址和令牌不回显。添加订阅不会替换其他来源。', 'Manage each source independently. URLs and tokens stay hidden; adding a subscription preserves other sources.') }}</p></div>
        <button type="button" class="btn btn-primary" :disabled="busy || !status?.installed" @click="openSubscription()">{{ text('添加订阅', 'Add subscription') }}</button>
      </div>
      <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-600">
        <div class="flex flex-wrap items-center gap-3">
          <label for="subscription-download-mode" class="text-sm font-medium">{{ text('订阅下载模式', 'Subscription download mode') }}</label>
          <select id="subscription-download-mode" v-model="downloadMode" class="input w-auto" :disabled="busy" @change="downloadModeDirty = true">
            <option value="auto">{{ text('自动：代理失败后直连', 'Auto: proxy with direct fallback') }}</option>
            <option value="proxy">{{ text('仅通过 Mihomo', 'Mihomo proxy only') }}</option>
            <option value="direct">{{ text('仅直连', 'Direct only') }}</option>
          </select>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !downloadModeDirty" @click="saveDownloadMode">{{ text('保存下载模式', 'Save download mode') }}</button>
        </div>
        <p class="mt-2 text-xs text-gray-500">{{ text('仅影响订阅文件下载，不改变打票或账号业务出口。自动模式在代理下载失败或内容无效时尝试直连；仅代理模式不会回退直连。', 'Only affects subscription downloads, not ticket or account traffic. Auto retries direct after a failed or invalid proxy response; proxy-only never falls back to direct.') }}</p>
      </div>
      <div class="flex flex-wrap gap-3">
        <input v-model="search" type="search" class="input sm:max-w-xs" :placeholder="text('搜索订阅名称', 'Search subscription names')" :aria-label="text('搜索订阅', 'Search subscriptions')" />
        <div v-if="selected.length" class="flex flex-wrap items-center gap-2 text-sm">
          <span>{{ text('已选', 'Selected') }} {{ selected.length }}</span>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="batchSubscriptions('subscription_refresh')">{{ text('更新所选', 'Refresh selected') }}</button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="batchSubscriptions('subscription_enable')">{{ text('启用所选', 'Enable selected') }}</button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="batchSubscriptions('subscription_disable')">{{ text('停用所选', 'Disable selected') }}</button>
          <button type="button" class="btn btn-danger btn-sm" :disabled="busy" @click="confirmRemoval(selected.map(id => 'subscription_remove/' + id))">{{ text('移除所选', 'Remove selected') }}</button>
        </div>
      </div>
      <DataTable v-model:selected-keys="selected" :columns="subscriptionColumns" :data="filteredSubscriptions" row-key="id" selectable :loading="loading">
        <template #cell-label="{ row }"><span class="font-medium">{{ row.label }}</span><code class="ml-2 text-xs text-gray-400">{{ row.id.slice(0, 8) }}</code></template>
        <template #cell-enabled="{ row }"><span class="badge" :class="row.enabled ? 'badge-success' : 'badge-gray'">{{ row.enabled ? text('启用', 'Enabled') : text('停用', 'Disabled') }}</span></template>
        <template #cell-nodes="{ row }">{{ row.cached ? row.nodes : text('待更新', 'Not refreshed') }}</template>
        <template #cell-updated_at="{ row }">{{ row.updated_at ? new Date(row.updated_at).toLocaleString() : '—' }}</template>
        <template #cell-actions="{ row }">
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !row.enabled" @click="operate('subscription_refresh/' + row.id)">{{ text('更新', 'Refresh') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="openSubscription(row)">{{ text('编辑', 'Edit') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="operate((row.enabled ? 'subscription_disable/' : 'subscription_enable/') + row.id)">{{ row.enabled ? text('停用', 'Disable') : text('启用', 'Enable') }}</button>
            <button type="button" class="btn btn-danger btn-sm" :disabled="busy" @click="confirmRemoval(['subscription_remove/' + row.id])">{{ text('移除', 'Remove') }}</button>
          </div>
        </template>
        <template #empty><p class="p-8 text-center text-sm text-gray-500">{{ text('暂无匹配的订阅。点击“添加订阅”导入 Clash / Mihomo YAML 来源。', 'No matching subscriptions. Add a Clash / Mihomo YAML source to get started.') }}</p></template>
      </DataTable>
      <p v-if="status?.subscriptions && !status.subscription_items" class="text-sm text-amber-700">{{ text('当前后端尚不支持订阅列表，请升级后端后使用逐条管理。', 'This backend does not support subscription lists yet. Upgrade it to manage individual sources.') }}</p>
    </template>

    <template v-if="section === 'dynamic' || section === 'nodes'">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="font-semibold">{{ section === 'dynamic' ? text('动态代理', 'Dynamic proxies') : text('节点管理', 'Node management') }}</h2><p class="mt-1 text-xs text-gray-500">{{ text('测试连接与质量检测通过独立内核只经该节点出口，不调用模型，也不改变节点状态；“检测”会按结果标记检测失败或恢复节点。订阅节点从所属订阅移除；动态代理可单独删除。', 'Test connection and quality check route through an isolated kernel using only that node; they call no models and never change node state. “Probe” marks the node failed or recovers it. Remove subscription nodes through their source; dynamic proxies can be deleted individually.') }}</p></div>
        <div class="flex flex-wrap gap-2">
          <button type="button" class="btn btn-secondary" :disabled="!status?.installed || batchTesting || !nodeBatchTargets.length" @click="batchTestNodes">{{ t('admin.proxies.testConnection') }}</button>
          <button type="button" class="btn btn-secondary" :disabled="!status?.installed || batchQualityChecking || !nodeBatchTargets.length" @click="batchCheckNodeQuality">{{ t('admin.proxies.batchQualityCheck') }}</button>
          <button type="button" class="btn btn-secondary" :disabled="busy || !selectedNodeTargets.length" @click="nodeEditDialog = true">{{ text('批量编辑', 'Batch edit') }}</button>
          <button type="button" class="btn btn-primary" :disabled="busy || !status?.running || !selectedNodeTargets.length" @click="openStaticProxyDialog(selectedNodeTargets)">{{ text('批量添加为静态代理', 'Add selected as static proxies') }}</button>
          <button v-if="section === 'dynamic'" type="button" class="btn btn-primary" :disabled="busy || !status?.installed" @click="dynamicDialog = true">{{ text('导入动态代理', 'Import dynamic proxies') }}</button>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-3">
        <input v-model="search" type="search" class="input sm:max-w-xs" :placeholder="text('搜索节点名称或地区', 'Search node names or regions')" :aria-label="text('搜索节点', 'Search nodes')" />
        <select v-model="nodeState" class="input w-auto" :aria-label="text('节点状态', 'Node status')">
          <option value="">{{ text('全部状态', 'All statuses') }}</option><option v-for="state in nodeStates" :key="state" :value="state">{{ stateLabel(state) }}</option>
        </select>
        <select v-if="section === 'nodes'" v-model="nodeSource" class="input w-auto" :aria-label="text('节点来源', 'Node source')">
          <option value="">{{ text('全部来源', 'All sources') }}</option><option value="dynamic">{{ text('动态代理', 'Dynamic proxies') }}</option>
          <option v-for="source in status?.subscription_items || []" :key="source.id" :value="source.id">{{ source.label }}</option>
        </select>
        <span class="text-xs text-gray-500" data-testid="node-batch-scope">{{ selectedNodes.length ? text(`批量检测已选的 ${nodeBatchTargets.length} 个节点`, `Batch checks cover ${nodeBatchTargets.length} selected nodes`) : text(`未选择时批量检测当前筛选的全部 ${nodeBatchTargets.length} 个节点`, `Without a selection, batch checks cover all ${nodeBatchTargets.length} filtered nodes`) }}</span>
      </div>
      <DataTable v-model:selected-keys="selectedNodes" :columns="nodeColumns" :data="pagedNodes" row-key="name" selectable :loading="loading">
        <template #cell-display_name="{ row }"><span>{{ row.display_name || row.name }}</span><span v-if="row.static_proxy" class="badge badge-primary ml-2">{{ text('已添加静态代理', 'Static proxy added') }}</span></template>
        <template #cell-source="{ row }">{{ nodeSourceLabel(row) }}</template>
        <template #cell-state="{ row }"><span class="badge" :class="row.state === 'enabled' ? 'badge-success' : 'badge-gray'">{{ stateLabel(row.state) }}</span></template>
        <template #cell-country_code="{ row }">{{ row.dynamic ? text('供应商控制', 'Provider managed') : row.country_code || text('未知', 'Unknown') }}</template>
        <template #cell-check="{ row }">
          <div class="flex flex-col gap-1" data-testid="node-check">
            <span v-if="row.check?.latency_status === 'failed'" class="badge badge-danger" :title="row.check.latency_message || undefined">{{ t('admin.proxies.latencyFailed') }}</span>
            <span v-else-if="typeof row.check?.latency_ms === 'number'" :class="['badge', row.check.latency_ms < 200 ? 'badge-success' : 'badge-warning']">{{ row.check.latency_ms }}ms</span>
            <span v-else class="text-sm text-gray-400">-</span>
            <div v-if="typeof row.check?.quality_checked === 'number'" class="flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400" :title="row.check.quality_summary || undefined">
              <span>{{ t('admin.proxies.qualityInline', { grade: row.check.quality_grade || '-', score: row.check.quality_score ?? '-' }) }}</span>
              <span class="badge" :class="proxyQualityOverallClass(row.check.quality_status)">{{ t(proxyQualityOverallLabelKey(row.check.quality_status)) }}</span>
            </div>
            <span v-if="row.check && checkExit(row.check)" class="text-xs text-gray-500 dark:text-gray-400" :title="checkTitle(row)">{{ checkExit(row.check) }}</span>
          </div>
        </template>
        <template #cell-actions="{ row }">
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-primary btn-sm" :disabled="busy || !status?.running || row.state !== 'enabled'" @click="openStaticProxyDialog([row])">{{ text('添加为静态代理', 'Add as static proxy') }}</button>
            <button v-if="row.static_proxy" type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="removeStaticProxy(row)">{{ text('移除静态代理', 'Remove static proxy') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!status?.installed || testingNodes.has(row.name)" :aria-busy="testingNodes.has(row.name)" @click="testNode(row)">{{ testingNodes.has(row.name) ? t('admin.proxies.testing') : t('admin.proxies.testConnection') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!status?.installed || qualityNodes.has(row.name)" :aria-busy="qualityNodes.has(row.name)" @click="checkNodeQuality(row)">{{ qualityNodes.has(row.name) ? t('admin.proxies.testing') : t('admin.proxies.qualityCheck') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !status?.running" :title="text('经运行中的内核测速：失败则标记为检测失败，成功则恢复节点', 'Measures through the running kernel: failure marks the node failed, success recovers it')" @click="operate('probe/' + row.name)">{{ text('检测', 'Probe') }}</button>
            <button v-if="!row.dynamic" type="button" class="btn btn-secondary btn-sm" :disabled="busy || !status?.running" @click="operate('country_probe/' + row.name)">{{ text('检测地区', 'Check region') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || row.state === 'country_excluded'" @click="operate((row.state === 'enabled' ? 'disable/' : 'recover/') + row.name)">{{ row.state === 'enabled' ? text('停用', 'Disable') : text('恢复', 'Recover') }}</button>
            <button v-if="row.dynamic" type="button" class="btn btn-danger btn-sm" :disabled="busy" @click="confirmRemoval(['dynamic_remove/' + row.name])">{{ text('移除', 'Remove') }}</button>
          </div>
        </template>
      </DataTable>
      <div class="flex items-center justify-between text-sm">
        <span>{{ text('共', 'Total') }} {{ filteredNodes.length }} {{ text('个节点', 'nodes') }} · {{ page }} / {{ totalPages }}</span>
        <div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="page <= 1" @click="page--">{{ text('上一页', 'Previous') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="page >= totalPages" @click="page++">{{ text('下一页', 'Next') }}</button></div>
      </div>
    </template>

    <template v-if="section === 'kernel'">
      <h2 class="font-semibold">{{ text('内核与地区规则', 'Kernel and region rules') }}</h2>
      <p class="text-sm text-gray-500">{{ text('这些设置只影响当前服务器。应用滚动更新保留内核；手动停止会中断使用本机出口的连接。', 'These settings apply only to the selected server. Application rolling updates retain the kernel; manually stopping it interrupts connections using its exits.') }}</p>
      <div class="flex flex-wrap gap-2">
        <button v-if="status && !status.installed" type="button" class="btn btn-primary" :disabled="busy || !status.supported" @click="operate('install')">{{ text('检测并安装', 'Install kernel') }}</button>
        <button v-else-if="!status?.running" type="button" class="btn btn-secondary" :disabled="busy || !status?.nodes" @click="operate('start')">{{ text('启动内核', 'Start kernel') }}</button>
        <button v-if="status?.running" type="button" class="btn btn-danger" :disabled="busy" @click="confirmStop">{{ text('停止内核', 'Stop kernel') }}</button>
      </div>
      <details class="rounded-xl border border-gray-200 p-4 dark:border-dark-600">
        <summary class="cursor-pointer text-sm font-medium">{{ text('共享站点的私网出口设置', 'Private outbound settings for shared sites') }}</summary>
        <div class="mt-4 space-y-3">
          <p class="text-xs text-gray-500">{{ text('默认只允许本机检测。要供共享数据库的其他服务器使用，需填写本机私网地址和允许来源；不会监听公网，控制接口始终仅本机可用。已有静态代理时不能修改。', 'Local checks are available by default. To share exits with other servers, configure this server’s private address and allowed sources. Public listeners are prohibited; the control API remains local. Remove existing static bindings before changing these settings.') }}</p>
          <label class="block text-sm">{{ text('本机私网地址', 'This server’s private IP') }}<input v-model="privateHost" class="input mt-1 w-full" autocomplete="off" placeholder="10.0.0.10" /></label>
          <label class="block text-sm">{{ text('允许访问的私网 CIDR（每行一个）', 'Allowed private source CIDRs (one per line)') }}<textarea v-model="allowedCIDRs" class="input mt-1 w-full font-mono text-xs" rows="3" placeholder="10.0.0.0/24" /></label>
          <button type="button" class="btn btn-secondary" :disabled="busy" @click="saveExposure">{{ text('保存私网设置', 'Save private network settings') }}</button>
        </div>
      </details>
      <label v-if="status?.installed" class="flex items-center gap-2 text-sm"><input type="checkbox" :checked="status.use_once" :disabled="busy" @change="operate(status.use_once ? 'once_off' : 'once_on')" />{{ text('打票节点用后移出（需手动恢复）', 'Retire harvest nodes after use (manual recovery)') }}</label>
      <MihomoCountryFilter v-if="status?.installed" :filter="status.country_filter" :codes="status.country_codes || []" :nodes="status.node_states || []" :busy="busy" @save="operate('country_filter', { country_filter: $event })" @scan="operate('country_scan')" />
      <p class="text-xs text-gray-500">{{ text('这里的配置独立保存。采票出口在本页「采票出口」中选择，账号业务代理在账号设置中选择。', 'Configuration is saved here independently. Select the harvest exit in the Harvest Exit tab and account proxies in account settings.') }}</p>
    </template>
    <p v-if="status && !status.installed && section !== 'kernel'" class="text-sm text-amber-700">{{ text('请先在“内核与规则”中安装内核。', 'Install the kernel in Kernel and rules first.') }}</p>

    <BaseDialog :show="staticProxyDialog" :title="text('添加为静态代理', 'Add as static proxies')" @close="!pending && (staticProxyDialog = false)">
      <div class="space-y-3 text-sm">
        <p>{{ text(`将 ${staticProxyTargets.length} 个节点添加到“静态代理 / IP 代理池”，之后可在账号设置中单独选择。已添加的节点会复用现有代理。`, `Add ${staticProxyTargets.length} nodes to the static proxy pool, then select them in individual account settings. Existing proxies will be reused.`) }}</p>
        <p class="text-gray-500">{{ text('固定使用所选节点。Mihomo 停止、节点停用或从来源移除后，该代理将不可用；不会自动切换到其他节点。', 'Each proxy uses only its selected node. It becomes unavailable when Mihomo stops or the node is disabled or removed; it will not switch to another node.') }}</p>
        <p v-if="staticProxyTargets.some(node => node.dynamic)" class="text-amber-700">{{ text('包含动态代理：固定的是代理配置，实际公网 IP 仍由供应商控制，可能随连接变化。', 'Includes dynamic proxies: the proxy configuration is fixed, but the provider may change the public IP between connections.') }}</p>
        <ul class="max-h-48 overflow-auto rounded-lg bg-gray-50 p-3 dark:bg-dark-700"><li v-for="node in staticProxyTargets" :key="node.name">{{ node.display_name || node.name }}</li></ul>
        <p v-if="staticProxyTargets.some(node => node.state !== 'enabled')" role="alert" class="text-amber-700">{{ text('请先恢复所选节点，并确认地区规则允许使用这些节点。', 'Restore selected nodes and allow their regions before adding them.') }}</p>
        <p v-if="staticProxyError" role="alert" class="text-red-600">{{ staticProxyError }}</p>
      </div>
      <template #footer><button type="button" class="btn btn-primary" :disabled="busy || !staticProxyTargets.length || staticProxyTargets.some(node => node.state !== 'enabled')" @click="addStaticProxies">{{ pending ? text('正在添加…', 'Adding…') : text('确认添加', 'Confirm add') }}</button></template>
    </BaseDialog>
    <BaseDialog :show="nodeEditDialog" :title="text('批量编辑节点', 'Batch edit nodes')" @close="!pending && (nodeEditDialog = false)">
      <div class="space-y-3 text-sm">
        <p>{{ text(`仅修改已选择的 ${selectedNodeTargets.length} 个节点。`, `Only the ${selectedNodeTargets.length} selected nodes will be changed.`) }}</p>
        <label class="block">{{ text('操作', 'Action') }}<select v-model="nodeEditAction" class="input mt-2" data-testid="node-batch-action"><option value="recover">{{ text('恢复节点', 'Recover nodes') }}</option><option value="disable">{{ text('停用节点', 'Disable nodes') }}</option><option value="static">{{ text('添加为静态代理', 'Add as static proxies') }}</option></select></label>
        <p class="text-gray-500">{{ text('恢复操作仍受地区规则限制；停用节点会同时停止对应静态代理的转发。', 'Recovery still follows region rules. Disabling a node also stops forwarding through its static proxy.') }}</p>
        <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
      </div>
      <template #footer><button type="button" class="btn btn-primary" :disabled="busy || !selectedNodeTargets.length" @click="applyNodeEdit">{{ text('应用所选', 'Apply to selected') }}</button></template>
    </BaseDialog>
    <BaseDialog :show="subscriptionDialog" :title="editing ? text('编辑订阅', 'Edit subscription') : text('添加订阅', 'Add subscription')" @close="subscriptionDialog = false">
      <div class="space-y-4">
        <label class="block text-sm">{{ text('订阅名称（单条订阅，可选）', 'Subscription name (single source, optional)') }}<input v-model="subscriptionName" maxlength="80" class="input mt-2" data-testid="subscription-name" /></label>
        <label class="block text-sm" for="mihomo-subscriptions">{{ editing ? text('新订阅地址（留空只修改名称）', 'New URL (leave blank to rename only)') : text('机场订阅地址（每行一个）', 'Subscription URLs (one per line)') }}</label>
        <textarea id="mihomo-subscriptions" v-model="subscriptions" class="input w-full font-mono text-xs" rows="5" autocomplete="off" spellcheck="false" />
        <p class="text-xs text-gray-500">{{ text('支持 Clash / Mihomo YAML。地址含密钥时请勿填入名称；已保存的地址不会回显。', 'Requires Clash / Mihomo YAML. Keep secrets out of names; saved URLs are never displayed.') }}</p>
        <input v-if="!editing" type="file" accept=".txt,text/plain" :aria-label="text('导入订阅 TXT', 'Import subscription TXT')" @change="importFile" />
        <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      </div>
      <template #footer><button type="button" class="btn btn-primary" :disabled="busy || (!subscriptions.trim() && !editing)" @click="saveSubscription">{{ text('保存并应用', 'Save and apply') }}</button></template>
    </BaseDialog>
    <BaseDialog :show="dynamicDialog" :title="text('导入动态代理', 'Import dynamic proxies')" @close="dynamicDialog = false">
      <div class="space-y-3">
        <label class="block text-sm" for="mihomo-dynamic-protocol">{{ text('无协议前缀时使用', 'Default protocol') }}</label>
        <select id="mihomo-dynamic-protocol" v-model="dynamicProtocol" class="input"><option value="http">HTTP</option><option value="https">HTTPS</option><option value="socks5">SOCKS5</option><option value="socks5h">SOCKS5H</option></select>
        <label class="block text-sm" for="mihomo-dynamic-proxies">{{ text('动态代理（每行一个）', 'Dynamic proxies (one per line)') }}</label>
        <textarea id="mihomo-dynamic-proxies" v-model="dynamicProxies" class="input w-full font-mono text-xs" rows="6" autocomplete="off" spellcheck="false" />
        <details class="text-xs text-gray-500"><summary>{{ text('支持的输入格式', 'Supported formats') }}</summary><pre class="mt-2 whitespace-pre-wrap">hostname:port:username:password
username:password:hostname:port
username:password@hostname:port
hostname:port@username:password
http://username:password@hostname:port</pre></details>
        <label class="flex items-center gap-2 text-sm"><input v-model="replaceDynamic" type="checkbox" />{{ text('替换已保存的动态代理（不勾选则追加）', 'Replace saved dynamic proxies (otherwise append)') }}</label>
        <p class="text-xs text-gray-500">{{ text('不会删除机场订阅。更换出口的国家／地区仍由供应商控制。', 'Subscriptions are preserved. Exit regions remain controlled by the provider.') }}</p>
        <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      </div>
      <template #footer><button type="button" class="btn btn-primary" :disabled="busy || !dynamicProxies.trim()" @click="saveDynamic">{{ text('应用动态代理', 'Apply dynamic proxies') }}</button></template>
    </BaseDialog>
    <ConfirmDialog :show="!!confirmation" :title="text('确认变更代理来源', 'Confirm proxy source changes')" :message="confirmation?.message || ''" danger @cancel="confirmation = undefined" @confirm="confirmActions" />
    <ProxyQualityReportDialog :show="!!qualityReport" :name="qualityReportName" :report="qualityReport" @close="qualityReport = null" />
  </div>
</template>
<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, watch, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { apiClient as sharedAPIClient } from '@/api/client'
import type { AxiosRequestConfig } from 'axios'
import { useAppStore } from '@/stores/app'
import DataTable from '@/components/common/DataTable.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ProxyQualityReportDialog from './ProxyQualityReportDialog.vue'
import MihomoCountryFilter from './MihomoCountryFilter.vue'
import type { CountryFilter, CountryNode } from './mihomoCountry'
import type { ProxyQualityCheckResult } from '@/types'
import { proxyQualityOverallClass, proxyQualityOverallLabelKey, summarizeProxyQuality } from './proxyQuality'
const props = withDefaults(defineProps<{ section?: 'subscriptions' | 'dynamic' | 'nodes' | 'kernel' }>(), { section: 'subscriptions' })
const emit = defineEmits<{ 'static-proxies-added': [] }>()
const { locale, t } = useI18n()
const appStore = useAppStore()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
type SubscriptionDownloadMode = 'auto' | 'proxy' | 'direct'
interface WarmPoolStatus { ready_subscription?: number; ready_dynamic?: number; target: number; ready: number; checking: number; cooling: number; failure_reasons?: Record<string, number> }
interface Subscription { id: string; label: string; enabled: boolean; nodes: number; cached: boolean; updated_at?: string }
// Latest isolated-kernel check of a node, shaped like the static proxy list's latency fields.
interface NodeCheck { checked_at: number; latency_status?: 'success' | 'failed'; latency_ms?: number; latency_message?: string; ip_address?: string; country?: string; country_code?: string; region?: string; city?: string; quality_status?: string; quality_score?: number; quality_grade?: string; quality_summary?: string; quality_checked?: number }
interface NodeTestResult { success: boolean; message: string; latency_ms?: number; ip_address?: string; country?: string; country_code?: string; region?: string; city?: string }
interface ManagedNode extends CountryNode { subscription_ids?: string[]; check?: NodeCheck; static_proxy?: boolean }
interface Status { host_id?: string; static_import_ready?: boolean; exposure?: { listen: string; advertise: string; allowed_cidrs: string[] }; subscription_download_mode?: SubscriptionDownloadMode; bps_warm_pool?: WarmPoolStatus; bps_ip_warm_pool?: WarmPoolStatus; installed: boolean; running: boolean; busy: boolean; supported: boolean; phase: string; error?: string; nodes: number; subscriptions: number; subscription_items?: Subscription[]; dynamic_proxies?: number; endpoint: string; use_once?: boolean; node_states?: ManagedNode[]; country_filter?: CountryFilter; country_codes?: string[] }
interface Payload { subscriptions?: string[]; dynamic_proxies?: string[]; country_filter?: CountryFilter; name?: string; confirm_stop?: boolean }
const status = ref<Status>()
const hostHeaders = () => status.value?.host_id ? { 'X-Mihomo-Host': status.value.host_id } : {}
const apiClient = {
  get<T>(url: string) { return sharedAPIClient.get<T>(url, { headers: hostHeaders() }) },
  post<T>(url: string, data?: unknown, options: AxiosRequestConfig = {}) { return sharedAPIClient.post<T>(url, data, { ...options, headers: { ...options.headers, ...hostHeaders() } }) },
  put<T>(url: string, data?: unknown) { return sharedAPIClient.put<T>(url, data, { headers: hostHeaders() }) }
}
const privateHost = ref('')
const allowedCIDRs = ref('')
watch(() => status.value?.host_id, () => { privateHost.value = status.value?.exposure?.listen || ''; allowedCIDRs.value = status.value?.exposure?.allowed_cidrs?.join('\n') || '' })
const loading = ref(true)
const pending = ref(false)
const busy = computed(() => pending.value || !!status.value?.busy)
const error = ref('')
const success = ref('')
const subscriptions = ref('')
const subscriptionName = ref('')
const subscriptionDialog = ref(false)
const downloadMode = ref<SubscriptionDownloadMode>('auto')
const downloadModeDirty = ref(false)
watch(() => status.value?.subscription_download_mode, mode => { if (!downloadModeDirty.value) downloadMode.value = mode || 'auto' })
const editing = ref<Subscription>()
const dynamicDialog = ref(false)
const dynamicProxies = ref('')
const dynamicProtocol = ref('http')
const replaceDynamic = ref(false)
const selected = ref<Array<string | number>>([])
const selectedNodes = ref<Array<string | number>>([])
const nodeEditDialog = ref(false)
const nodeEditAction = ref<'recover' | 'disable' | 'static'>('recover')
const staticProxyDialog = ref(false)
const staticProxyTargets = ref<ManagedNode[]>([])
const staticProxyError = ref('')
const testingNodes = ref(new Set<string>())
const qualityNodes = ref(new Set<string>())
const batchTesting = ref(false)
const batchQualityChecking = ref(false)
const qualityReport = ref<ProxyQualityCheckResult | null>(null)
const qualityReportName = ref('')
// Each check starts a private kernel and may queue behind other checks on the server.
const nodeTestTimeout = 60000
const nodeQualityTimeout = 150000
const search = ref('')
const nodeState = ref('')
const nodeSource = ref('')
const page = ref(1)
const nodeStates = ['enabled', 'disabled', 'failed', 'used', 'country_excluded']
const confirmation = ref<{ actions: string[]; message: string; payload?: Payload }>()
const stateLabel = (state: string) => ({ enabled: text('启用', 'Enabled'), disabled: text('停用', 'Disabled'), failed: text('检测失败', 'Failed'), used: text('已使用', 'Used'), country_excluded: text('地区已排除', 'Region excluded') }[state] || state)
const warmPools = computed(() => [
  { source: 'Mihomo', pool: status.value?.bps_warm_pool },
  { source: text('IP 管理', 'IP Management'), pool: status.value?.bps_ip_warm_pool }
].flatMap(item => item.pool && (item.pool.target > 0 || item.pool.ready > 0) ? [{ source: item.source, ...item.pool }] : []))
const subscriptionColumns = computed(() => [
  { key: 'label', label: text('名称', 'Name'), sortable: true }, { key: 'enabled', label: text('状态', 'Status') },
  { key: 'nodes', label: text('节点数', 'Nodes') }, { key: 'updated_at', label: text('最后更新', 'Last refreshed') }, { key: 'actions', label: text('操作', 'Actions') }
])
const nodeColumns = computed(() => [
  { key: 'display_name', label: text('名称', 'Name') }, { key: 'source', label: text('来源', 'Source') },
  { key: 'state', label: text('状态', 'Status') }, { key: 'country_code', label: text('地区', 'Region') },
  { key: 'check', label: t('admin.proxies.columns.latency') }, { key: 'actions', label: text('操作', 'Actions') }
])
const filteredSubscriptions = computed(() => (status.value?.subscription_items || []).filter(s => (s.label + s.id).toLowerCase().includes(search.value.trim().toLowerCase())))
const filteredNodes = computed(() => (status.value?.node_states || []).filter(n =>
  (props.section !== 'dynamic' || n.dynamic) && (!nodeState.value || n.state === nodeState.value) &&
  (!nodeSource.value || (nodeSource.value === 'dynamic' ? n.dynamic : n.subscription_ids?.includes(nodeSource.value))) &&
  ((n.display_name || n.name) + ' ' + (n.country_code || '')).toLowerCase().includes(search.value.trim().toLowerCase())
))
const totalPages = computed(() => Math.max(1, Math.ceil(filteredNodes.value.length / 50)))
const pagedNodes = computed(() => filteredNodes.value.slice((page.value - 1) * 50, page.value * 50))
const selectedNodeTargets = computed(() => {
  const selected = new Set(selectedNodes.value)
  return filteredNodes.value.filter(node => selected.has(node.name))
})
// Like the static proxy list: selected nodes, otherwise every node matching the filters.
const nodeBatchTargets = computed(() => {
  if (!selectedNodes.value.length) return filteredNodes.value
  const chosen = new Set(selectedNodes.value)
  return filteredNodes.value.filter(n => chosen.has(n.name))
})
watch([search, nodeState, nodeSource, () => props.section], () => { page.value = 1; selected.value = []; selectedNodes.value = [] })
watch(() => props.section, () => { search.value = ''; nodeSource.value = ''; nodeState.value = '' })
watch(totalPages, count => { page.value = Math.min(page.value, count) })
function nodeSourceLabel(node: ManagedNode) {
  if (node.dynamic) return text('动态代理', 'Dynamic proxy')
  return node.subscription_ids?.map(id => status.value?.subscription_items?.find(s => s.id === id)?.label || id.slice(0, 8)).join(', ') || text('订阅（待更新来源）', 'Subscription (refresh to identify)')
}
function openStaticProxyDialog(nodes: ManagedNode[]) {
  if (!status.value?.static_import_ready) { error.value = text('先在高级设置中配置本机私网出口和允许来源，再导入共享代理。', 'Configure this server’s private listener and allowed sources in advanced settings before importing shared proxies.'); return }
  staticProxyTargets.value = [...nodes]
  staticProxyError.value = ''
  staticProxyDialog.value = true
}
async function addStaticProxies() {
  if (pending.value || !staticProxyTargets.value.length) return
  pending.value = true
  staticProxyError.value = ''
  let created = 0
  let existing = 0
  const failedNames = new Set<string>()
  const targets = [...staticProxyTargets.value]
  try {
    for (let offset = 0; offset < targets.length; offset += 100) {
      const { data } = await apiClient.post<{ created: unknown[]; existing: unknown[]; failed: Array<{ name: string; reason: string }> }>('/admin/system/mihomo/nodes/static-proxies', { names: targets.slice(offset, offset + 100).map(node => node.name) }, { timeout: 180000 })
      created += data.created.length
      existing += data.existing.length
      for (const failure of data.failed) failedNames.add(failure.name)
      if (disposed) return
    }
    emit('static-proxies-added')
    const summary = text(`新增 ${created} 个静态代理，复用 ${existing} 个，失败 ${failedNames.size} 个。可在账号设置中选择代理。`, `Created ${created} static proxies, reused ${existing}, failed ${failedNames.size}. Select them in account settings.`)
    if (failedNames.size) {
      staticProxyTargets.value = targets.filter(node => failedNames.has(node.name))
      staticProxyError.value = summary
      appStore.showWarning(summary)
    } else {
      staticProxyDialog.value = false
      selectedNodes.value = []
      appStore.showSuccess(summary)
    }
  } catch (cause) {
    staticProxyError.value = causeMessage(cause, text('添加未完成，请重试；已创建的代理会自动复用。', 'Could not finish adding proxies. Retry to reuse any proxies already created.'))
    emit('static-proxies-added')
  } finally {
    pending.value = false
    if (!disposed) await refresh()
  }
}
async function removeStaticProxy(node: ManagedNode) {
  if (pending.value || !window.confirm(text('确认释放此静态代理？仍被账号引用时将拒绝操作。', 'Remove this static proxy? The operation is rejected while accounts still use it.'))) return
  pending.value = true
  error.value = ''
  try {
    await apiClient.post('/admin/system/mihomo/nodes/static-proxies/remove', { names: [node.name] }, { timeout: 180000 })
    emit('static-proxies-added')
    appStore.showSuccess(text('静态代理已移除，对应监听已关闭。', 'Static proxy removed and its listener closed.'))
  } catch (cause) {
    error.value = causeMessage(cause, text('移除失败；请先取消账号对该代理的引用。', 'Removal failed; remove account references to this proxy first.'))
  } finally {
    pending.value = false
    if (!disposed) await refresh()
  }
}
async function applyNodeEdit() {
  const targets = [...selectedNodeTargets.value]
  if (nodeEditAction.value === 'static') {
    nodeEditDialog.value = false
    openStaticProxyDialog(targets)
    return
  }
  for (const node of targets) {
    if (!await operate(nodeEditAction.value + '/' + node.name)) return
    selectedNodes.value = selectedNodes.value.filter(name => name !== node.name)
  }
  nodeEditDialog.value = false
}
const checkExit = (check: NodeCheck) => [check.ip_address, check.country, check.city].filter(Boolean).join(' · ')
function checkTitle(node: ManagedNode) {
  const checked = node.check ? text('检测时间 ', 'Checked ') + new Date(node.check.checked_at * 1000).toLocaleString() : ''
  return node.dynamic ? checked + text('；动态代理每次连接可能更换出口', '; dynamic proxies may change exit on every connection') : checked
}
const nodeCheckPath = (name: string, check: 'test' | 'quality-check') => `/admin/system/mihomo/nodes/${encodeURIComponent(name)}/${check}`
const causeMessage = (cause: unknown, fallback: string) => (cause as { message?: string })?.message || fallback
function setNodeBusy(nodes: Ref<Set<string>>, name: string, active: boolean) {
  const next = new Set(nodes.value)
  if (active) next.add(name)
  else next.delete(name)
  nodes.value = next
}
// Show a result at once; the next status refresh returns the kernel's record.
function applyNodeCheck(name: string, update: (previous?: NodeCheck) => NodeCheck) {
  const node = status.value?.node_states?.find(n => n.name === name)
  if (node) node.check = update(node.check)
}
function previousQuality(check?: NodeCheck): Partial<NodeCheck> {
  if (typeof check?.quality_checked !== 'number') return {}
  return { quality_status: check.quality_status, quality_score: check.quality_score, quality_grade: check.quality_grade, quality_summary: check.quality_summary, quality_checked: check.quality_checked }
}
async function runNodeTest(node: ManagedNode): Promise<NodeTestResult> {
  setNodeBusy(testingNodes, node.name, true)
  try {
    const { data } = await apiClient.post<NodeTestResult>(nodeCheckPath(node.name, 'test'), undefined, { timeout: nodeTestTimeout })
    applyNodeCheck(node.name, previous => ({
      ...previousQuality(previous), checked_at: Math.floor(Date.now() / 1000), latency_status: data.success ? 'success' : 'failed', latency_message: data.message,
      ...(data.success ? { latency_ms: data.latency_ms, ip_address: data.ip_address, country: data.country, country_code: data.country_code, region: data.region, city: data.city } : {})
    }))
    return data
  } finally { setNodeBusy(testingNodes, node.name, false) }
}
async function runNodeQuality(node: ManagedNode): Promise<ProxyQualityCheckResult> {
  setNodeBusy(qualityNodes, node.name, true)
  try {
    const { data } = await apiClient.post<ProxyQualityCheckResult>(nodeCheckPath(node.name, 'quality-check'), undefined, { timeout: nodeQualityTimeout })
    const reachable = (data.items || []).some(item => item.target === 'base_connectivity' && item.status === 'pass')
    applyNodeCheck(node.name, previous => {
      const sameExit = !!data.exit_ip && previous?.ip_address === data.exit_ip
      return {
        checked_at: Math.floor(Date.now() / 1000), latency_status: reachable ? 'success' : 'failed', latency_ms: reachable && data.base_latency_ms ? data.base_latency_ms : undefined, latency_message: data.summary,
        ip_address: data.exit_ip, country: data.country, country_code: data.country_code, region: sameExit ? previous?.region : undefined, city: sameExit ? previous?.city : undefined,
        quality_status: summarizeProxyQuality(data), quality_score: data.score, quality_grade: data.grade, quality_summary: data.summary, quality_checked: data.checked_at
      }
    })
    return data
  } finally { setNodeBusy(qualityNodes, node.name, false) }
}
async function testNode(node: ManagedNode) {
  try {
    const result = await runNodeTest(node)
    if (result.success) appStore.showSuccess(result.latency_ms ? t('admin.proxies.proxyWorkingWithLatency', { latency: result.latency_ms }) : t('admin.proxies.proxyWorking'))
    else appStore.showError(result.message || t('admin.proxies.proxyTestFailed'))
  } catch (cause: unknown) {
    appStore.showError(causeMessage(cause, t('admin.proxies.failedToTest')))
  }
  if (!disposed) void refresh()
}
async function checkNodeQuality(node: ManagedNode) {
  try {
    const result = await runNodeQuality(node)
    if (disposed) return
    qualityReportName.value = node.display_name || node.name
    qualityReport.value = result
    appStore.showSuccess(t('admin.proxies.qualityCheckDone', { score: result.score, grade: result.grade }))
  } catch (cause: unknown) {
    appStore.showError(causeMessage(cause, t('admin.proxies.qualityCheckFailed')))
  }
  if (!disposed) void refresh()
}
async function runNodeBatch(nodes: ManagedNode[], concurrency: number, run: (node: ManagedNode) => Promise<void>) {
  let index = 0
  const worker = async () => {
    while (index < nodes.length && !disposed) await run(nodes[index++])
  }
  await Promise.all(Array.from({ length: Math.min(concurrency, nodes.length) }, worker))
}
async function batchTestNodes() {
  const targets = [...nodeBatchTargets.value]
  if (batchTesting.value || !targets.length) return
  batchTesting.value = true
  let succeeded = 0
  let failed = 0
  try {
    await runNodeBatch(targets, 3, async node => {
      if (testingNodes.value.has(node.name)) return
      try {
        if ((await runNodeTest(node)).success) succeeded++
        else failed++
      } catch { failed++ }
    })
    if (disposed) return
    const summary = text(`批量测试完成，共 ${succeeded + failed} 个节点：成功 ${succeeded} 个，失败 ${failed} 个`, `Tested ${succeeded + failed} nodes: ${succeeded} succeeded, ${failed} failed`)
    if (failed) appStore.showWarning(summary)
    else appStore.showSuccess(summary)
  } finally {
    batchTesting.value = false
    if (!disposed) void refresh()
  }
}
async function batchCheckNodeQuality() {
  const targets = [...nodeBatchTargets.value]
  if (batchQualityChecking.value || !targets.length) return
  batchQualityChecking.value = true
  const counts = { healthy: 0, warn: 0, challenge: 0, failed: 0 }
  try {
    await runNodeBatch(targets, 2, async node => {
      if (qualityNodes.value.has(node.name)) return
      try { counts[summarizeProxyQuality(await runNodeQuality(node))]++ } catch { counts.failed++ }
    })
    if (disposed) return
    const total = counts.healthy + counts.warn + counts.challenge + counts.failed
    const summary = text(`批量质量检测完成，共 ${total} 个节点：优质 ${counts.healthy} 个，告警 ${counts.warn} 个，挑战 ${counts.challenge} 个，异常 ${counts.failed} 个`, `Quality checked ${total} nodes: healthy ${counts.healthy}, warn ${counts.warn}, challenge ${counts.challenge}, abnormal ${counts.failed}`)
    if (counts.challenge || counts.failed) appStore.showWarning(summary)
    else appStore.showSuccess(summary)
  } finally {
    batchQualityChecking.value = false
    if (!disposed) void refresh()
  }
}
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
function scheduleRefresh() {
  if (timer) clearTimeout(timer)
  if (!disposed && !pending.value && (status.value?.busy || warmPools.value.some(p => p.target > 0))) timer = setTimeout(refresh, status.value?.busy ? 1500 : 5000)
}
async function refresh() {
  if (timer) clearTimeout(timer)
  try {
    const { data } = await apiClient.get<Status>('/admin/system/mihomo')
    if (!disposed) { status.value = data; error.value = '' }
  } catch { if (!disposed) error.value = text('无法读取内核状态', 'Cannot read kernel status') }
  finally { loading.value = false; scheduleRefresh() }
}
async function operate(action: string, payload: Payload = {}): Promise<boolean> {
  pending.value = true; error.value = ''; success.value = ''
  if (timer) clearTimeout(timer)
  try {
    status.value = (await apiClient.post<Status>('/admin/system/mihomo', { action, subscriptions: [], dynamic_proxies: [], append: false, ...payload })).data
    while (status.value.busy && !disposed) {
      await new Promise(resolve => setTimeout(resolve, 1500))
      if (disposed) return false
      status.value = (await apiClient.get<Status>('/admin/system/mihomo')).data
    }
    if (disposed) return false
    if (status.value.error) { error.value = status.value.error; return false }
    success.value = text('变更已应用', 'Changes applied')
    return true
  } catch (cause: unknown) {
    error.value = (cause as { message?: string })?.message || text('操作未完成，请重试', 'Operation failed. Please retry.')
    return false
  } finally { pending.value = false; scheduleRefresh() }
}
async function saveDownloadMode() {
  pending.value = true; error.value = ''; success.value = ''
  if (timer) clearTimeout(timer)
  try {
    status.value = (await apiClient.put<Status>('/admin/system/mihomo/download-mode', { mode: downloadMode.value })).data
    downloadModeDirty.value = false
    downloadMode.value = status.value.subscription_download_mode || 'auto'
    success.value = text('下载模式已保存', 'Download mode saved')
  } catch (cause: unknown) {
    error.value = (cause as { message?: string })?.message || text('无法保存下载模式', 'Cannot save download mode')
  } finally { pending.value = false; scheduleRefresh() }
}

function confirmStop() {
  confirmation.value = { actions: ['stop'], payload: { confirm_stop: true }, message: text('停止当前服务器的代理内核？正在使用这些出口的连接会中断。', 'Stop this server’s proxy kernel? Active connections using these exits will be interrupted.') }
}
async function saveExposure() {
  pending.value = true; error.value = ''; success.value = ''
  try {
    status.value = (await apiClient.put<Status>('/admin/system/mihomo/exposure', { listen: privateHost.value.trim(), advertise: privateHost.value.trim(), allowed_cidrs: allowedCIDRs.value.split(/\r?\n/).map(value => value.trim()).filter(Boolean) })).data
    success.value = text('私网设置已保存；创建静态代理时生效。', 'Private network settings saved; they apply when static proxies are created.')
  } catch (cause) { error.value = causeMessage(cause, text('无法保存私网设置', 'Could not save private network settings')) }
  finally { pending.value = false }
}
function openSubscription(source?: Subscription) {
  editing.value = source; subscriptionName.value = source?.label || ''; subscriptions.value = ''; error.value = ''; subscriptionDialog.value = true
}
async function saveSubscription() {
  const urls = subscriptions.value.split(/\r?\n/).map(s => s.trim()).filter(Boolean)
  const action = editing.value ? (urls.length ? 'subscription_update/' : 'subscription_rename/') + editing.value.id : 'subscription_add'
  if (await operate(action, { subscriptions: urls, name: subscriptionName.value.trim() })) { subscriptions.value = ''; subscriptionDialog.value = false }
}
function dynamicPayload(): Payload {
  return { dynamic_proxies: dynamicProxies.value.split(/\r?\n/).map(s => s.trim()).filter(Boolean).map(s => s.includes('://') ? s : dynamicProtocol.value + '://' + s) }
}
async function saveDynamic() {
  if (replaceDynamic.value && status.value?.dynamic_proxies) {
    confirmation.value = { actions: ['dynamic_replace'], payload: dynamicPayload(), message: text('将替换全部已保存动态代理，可能中断使用旧出口的会话。机场订阅会保留。', 'This replaces all saved dynamic proxies and may interrupt sessions using old exits. Subscriptions are preserved.') }
  } else if (await operate(replaceDynamic.value ? 'dynamic_replace' : 'dynamic_append', dynamicPayload())) { dynamicProxies.value = ''; dynamicDialog.value = false }
}
function confirmRemoval(actions: string[]) {
  confirmation.value = { actions, message: text('移除这些来源及其独有节点？其他来源会保留，正在使用被移除出口的会话可能受影响。移除最后一个来源后将拒绝代理流量。', 'Remove these sources and their exclusive nodes? Other sources are preserved; sessions using removed exits may be affected. Removing the last source rejects proxy traffic.') }
}
async function confirmActions() {
  const task = confirmation.value
  confirmation.value = undefined
  if (!task) return
  for (const action of task.actions) {
    if (!await operate(action, task.payload)) return
    selected.value = selected.value.filter(id => !action.endsWith('/' + id))
  }
  if (task.actions.includes('dynamic_replace')) { dynamicProxies.value = ''; dynamicDialog.value = false }
}
async function batchSubscriptions(action: string) {
  const targets = [...selected.value]
  for (const id of targets) {
    if (action === 'subscription_refresh' && !status.value?.subscription_items?.find(s => s.id === id)?.enabled) continue
    if (!await operate(action + '/' + id)) return
  }
  selected.value = []
}
async function importFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  if (file.size > 256 * 1024) { error.value = text('文件不能超过 256 KiB', 'File must be at most 256 KiB'); return }
  try { subscriptions.value = await file.text(); input.value = '' } catch { error.value = text('无法读取文件', 'Cannot read file') }
}
onMounted(refresh)
onUnmounted(() => { disposed = true; if (timer) clearTimeout(timer) })
</script>
