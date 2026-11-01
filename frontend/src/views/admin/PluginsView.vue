<template>
  <AppLayout>
    <div class="space-y-6">
      <AdminPageHeader :title="t('admin.plugins.title')" :description="t('admin.plugins.description')">
        <template #meta>
          <div class="flex flex-wrap gap-1.5">
            <UBadge color="neutral" variant="subtle" :label="t('admin.plugins.onlyOpenAI')" />
            <UBadge color="neutral" variant="subtle" :label="t('admin.plugins.noAccountCoupling')" />
          </div>
        </template>

        <template #actions>
          <!-- Hidden native picker behind the upload button (no Nuxt UI equivalent for a programmatic picker) -->
          <input
            ref="fileInput"
            class="hidden"
            type="file"
            accept=".s2plugin,application/zip"
            @change="handleFileSelected"
          />
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-refresh-cw"
            square
            :disabled="loading"
            :ui="{ leadingIcon: loading ? 'animate-spin' : '' }"
            :title="t('common.refresh')"
            :aria-label="t('common.refresh')"
            @click="loadPlugins"
          />
          <UButton
            icon="i-lucide-upload"
            :loading="uploading"
            :title="t('admin.plugins.uploadHint')"
            :label="uploading ? t('common.processing') : t('admin.plugins.upload')"
            @click="fileInput?.click()"
          />
        </template>
      </AdminPageHeader>

      <UAlert color="neutral" variant="subtle" icon="i-lucide-info">
        <template #description>
          <p>{{ t("admin.plugins.runtimeNotice") }}</p>
          <p class="mt-1">{{ t("admin.plugins.menuNotice") }}</p>
        </template>
      </UAlert>

      <div v-if="loading" class="flex min-h-48 items-center justify-center gap-2 text-sm text-muted">
        <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
        {{ t("common.loading") }}
      </div>

      <div v-else-if="plugins.length === 0" class="flex min-h-56 flex-col items-center justify-center px-6 text-center">
        <UIcon name="i-lucide-box" class="size-10 text-dimmed" />
        <p class="mt-3 font-medium text-highlighted">{{ t("admin.plugins.empty") }}</p>
        <p class="mt-1 max-w-lg text-sm text-muted">{{ t("admin.plugins.emptyHint") }}</p>
      </div>

      <!-- Installed plugins: one quiet row each (inbox list pattern) -->
      <ul v-else class="divide-y divide-default border-y border-default">
        <li
          v-for="plugin in plugins"
          :key="plugin.id"
          class="flex flex-col gap-4 py-5 lg:flex-row lg:items-start"
        >
          <div class="min-w-0 flex-1 space-y-2">
            <div class="flex flex-wrap items-center gap-2">
              <h3 class="truncate text-base font-semibold text-highlighted">{{ plugin.name }}</h3>
              <span class="font-mono text-xs text-muted">v{{ plugin.version }}</span>
              <UBadge :color="stateColor(plugin.state)" variant="subtle" :label="t(`admin.plugins.${plugin.state}`)" />
            </div>
            <p class="text-xs text-muted">
              {{ plugin.plugin_key }}<span v-if="plugin.author"> · {{ plugin.author }}</span>
            </p>
            <p v-if="plugin.description" class="text-sm text-default">{{ plugin.description }}</p>

            <div class="flex flex-wrap items-center gap-1.5 pt-1">
              <UBadge
                :color="compatibilityColor(plugin.compatibility.status)"
                variant="subtle"
                :label="`${t('admin.plugins.compatibility')}: ${t(`admin.plugins.${plugin.compatibility.status}`)}`"
              />
              <UBadge
                :color="plugin.runtime_healthy ? 'success' : 'neutral'"
                variant="subtle"
                :label="`${t('admin.plugins.runtime')}: ${plugin.runtime_healthy ? t('admin.plugins.healthy') : t('admin.plugins.unhealthy')}`"
              />
              <UBadge
                color="neutral"
                variant="outline"
                :label="`${t('admin.plugins.signature')}: ${t(`admin.plugins.${plugin.signature_status}`)}`"
              />
              <span v-if="plugin.compatibility.message" class="text-xs text-muted">{{ plugin.compatibility.message }}</span>
            </div>

            <dl class="flex flex-wrap gap-x-4 gap-y-1 text-xs">
              <div class="flex gap-1">
                <dt class="text-muted">{{ t("admin.plugins.currentVersion") }}</dt>
                <dd class="font-mono text-default">{{ plugin.compatibility.current_sub2api_version }}</dd>
              </div>
              <div class="flex gap-1">
                <dt class="text-muted">{{ t("admin.plugins.requiredVersion") }}</dt>
                <dd class="font-mono text-default">{{ plugin.compatibility.required_sub2api_version }}</dd>
              </div>
              <div class="flex gap-1">
                <dt class="text-muted">{{ t("admin.plugins.recommendedVersion") }}</dt>
                <dd class="font-mono text-default">{{ plugin.compatibility.recommended_sub2api_version || "-" }}</dd>
              </div>
            </dl>

            <p v-if="plugin.last_error" class="wrap-break-word text-xs text-error">{{ plugin.last_error }}</p>
            <p v-else-if="plugin.runtime_message" class="wrap-break-word text-xs text-muted">{{ plugin.runtime_message }}</p>
          </div>

          <div class="w-full space-y-2 lg:w-60">
            <div class="flex items-center justify-between gap-4 text-xs font-medium text-muted">
              <span>{{ t("admin.plugins.rollout") }}</span>
              <span class="font-mono text-default">{{ rolloutValues[plugin.id] ?? currentRollout(plugin) }}%</span>
            </div>
            <USlider
              :model-value="rolloutValues[plugin.id] ?? currentRollout(plugin)"
              :min="1"
              :max="100"
              :step="1"
              :disabled="hasEnabledBinding(plugin)"
              :aria-label="t('admin.plugins.rollout')"
              @update:model-value="(value: unknown) => setRollout(plugin.id, value as number | number[] | undefined)"
            />
          </div>

          <div class="flex flex-wrap items-center gap-1.5 lg:justify-end">
            <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-settings" :label="t('admin.plugins.configure')" @click="openConfiguration(plugin)" />
            <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-flask-conical" :disabled="busyID === plugin.id" :label="t('admin.plugins.test')" @click="testPlugin(plugin)" />
            <UButton
              v-if="hasEnabledBinding(plugin)"
              color="neutral"
              variant="outline"
              size="sm"
              icon="i-lucide-ban"
              :disabled="busyID === plugin.id"
              :label="t('admin.plugins.disable')"
              @click="disablePlugin(plugin)"
            />
            <UButton
              v-else
              size="sm"
              icon="i-lucide-play"
              :disabled="busyID === plugin.id || plugin.state === 'starting' || !plugin.compatibility.compatible"
              :label="t('admin.plugins.enable')"
              @click="enablePlugin(plugin)"
            />
            <UDropdownMenu :items="pluginMenuItems(plugin)" :content="{ align: 'end' }">
              <UButton color="neutral" variant="ghost" size="sm" square icon="i-lucide-ellipsis" :aria-label="t('common.more')" />
            </UDropdownMenu>
          </div>
        </li>
      </ul>

      <BaseDialog
        :show="configPlugin !== null"
        :title="t('admin.plugins.configTitle', { name: configPlugin?.name || '' })"
        width="full"
        @close="closeConfiguration"
      >
        <div class="relative min-h-[520px] overflow-hidden bg-elevated" :style="{ height: `${iframeHeight}px` }">
          <div v-if="uiLoading" class="absolute inset-0 z-10 flex items-center justify-center text-sm text-muted">
            {{ t("admin.plugins.loadingUI") }}
          </div>
          <div v-if="uiError" class="absolute inset-0 z-20 flex flex-col items-center justify-center p-8 text-center">
            <UIcon name="i-lucide-triangle-alert" class="size-10 text-warning" />
            <p class="mt-3 font-medium text-highlighted">{{ t("admin.plugins.uiUnavailable") }}</p>
            <p class="mt-1 max-w-xl text-sm text-muted">{{ uiError }}</p>
          </div>
          <iframe
            v-if="uiSession"
            ref="pluginFrame"
            :src="uiSession.url"
            sandbox="allow-scripts"
            referrerpolicy="no-referrer"
            class="h-full w-full border-0 bg-default"
            :title="t('admin.plugins.configTitle', { name: configPlugin?.name || '' })"
            @load="handlePluginFrameLoad"
          />
        </div>
      </BaseDialog>

      <TotpStepUpDialog :controller="pluginStepUp" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  adminAPI,
  type PluginInstallation,
  type PluginUISession,
} from "@/api/admin";
import { useAppStore } from "@/stores";
import AppLayout from "@/components/layout/AppLayout.vue";
import AdminPageHeader from "@/components/admin/page/AdminPageHeader.vue";
import BaseDialog from "@/components/common/BaseDialog.vue";
import type { DropdownMenuItem } from "@nuxt/ui";
import TotpStepUpDialog from "@/components/auth/TotpStepUpDialog.vue";
import {
  isStepUpBlocked,
  isStepUpCancelled,
  stepUpBlockReason,
  useStepUp,
} from "@/composables/useStepUp";

interface PluginBridgeMessage {
  source?: string;
  bridge_token?: string;
  type?: string;
  request_id?: string;
  config?: unknown;
  height?: unknown;
  level?: unknown;
  message?: unknown;
}

const { t } = useI18n();
const appStore = useAppStore();
const pluginStepUp = useStepUp();
const plugins = ref<PluginInstallation[]>([]);
const loading = ref(false);
const uploading = ref(false);
const busyID = ref<number | null>(null);
const fileInput = ref<HTMLInputElement | null>(null);
const rolloutValues = ref<Record<number, number>>({});
const configPlugin = ref<PluginInstallation | null>(null);
const uiSession = ref<PluginUISession | null>(null);
const pluginFrame = ref<HTMLIFrameElement | null>(null);
const uiLoading = ref(false);
const uiError = ref("");
const iframeHeight = ref(640);
const pluginFrameLoaded = ref(false);
const pendingBridgeRequests = new Map<string, number>();

function errorMessage(error: unknown): string {
  if (typeof error === "object" && error !== null && "message" in error) {
    return String(
      (error as { message?: unknown }).message || t("common.unknownError"),
    );
  }
  return t("common.unknownError");
}

function reportSensitiveActionError(error: unknown): void {
  if (isStepUpCancelled(error)) return;
  if (isStepUpBlocked(error)) {
    appStore.showError(
      stepUpBlockReason(error) === "STEP_UP_ADMIN_API_KEY_FORBIDDEN"
        ? t("stepUp.adminApiKeyForbidden")
        : t("stepUp.notEnabled"),
    );
    return;
  }
  appStore.showError(errorMessage(error));
}

async function loadPlugins(): Promise<void> {
  loading.value = true;
  try {
    plugins.value = await adminAPI.plugins.list();
    for (const plugin of plugins.value) {
      rolloutValues.value[plugin.id] = currentRollout(plugin);
    }
  } catch (error: unknown) {
    appStore.showError(errorMessage(error));
  } finally {
    loading.value = false;
  }
}

async function handleFileSelected(event: Event): Promise<void> {
  const target = event.target as HTMLInputElement;
  const file = target.files?.[0];
  target.value = "";
  if (!file || !file.name.toLowerCase().endsWith(".s2plugin")) {
    appStore.showError(t("admin.plugins.fileRequired"));
    return;
  }
  uploading.value = true;
  try {
    await pluginStepUp.run(() => adminAPI.plugins.upload(file));
    appStore.showSuccess(t("admin.plugins.uploadSuccess"));
    await loadPlugins();
  } catch (error: unknown) {
    reportSensitiveActionError(error);
  } finally {
    uploading.value = false;
  }
}

function currentRollout(plugin: PluginInstallation): number {
  return (
    plugin.bindings.find(
      (binding) => binding.capability === "openai.oauth.outbound_transport.v1",
    )?.rollout_percent || 100
  );
}

function hasEnabledBinding(plugin: PluginInstallation): boolean {
  return plugin.bindings.some((binding) => binding.enabled);
}

function setRollout(id: number, next: number | number[] | undefined): void {
  const value = Number(Array.isArray(next) ? next[0] : next);
  if (!Number.isFinite(value)) return;
  rolloutValues.value[id] = Math.min(100, Math.max(1, value));
}

// Row "…" menu: uninstall (blocked while a binding is enabled)
function pluginMenuItems(plugin: PluginInstallation): DropdownMenuItem[][] {
  return [[{
    label: t("admin.plugins.uninstall"),
    icon: "i-lucide-trash",
    color: "error",
    disabled: busyID.value === plugin.id || hasEnabledBinding(plugin),
    onSelect: () => uninstallPlugin(plugin),
  }]];
}

async function enablePlugin(plugin: PluginInstallation): Promise<void> {
  let acceptUntested = false;
  if (!plugin.compatibility.tested) {
    acceptUntested = window.confirm(t("admin.plugins.confirmUntested"));
    if (!acceptUntested) return;
  }
  busyID.value = plugin.id;
  try {
    await pluginStepUp.run(() =>
      adminAPI.plugins.enable(
        plugin.id,
        rolloutValues.value[plugin.id] || 100,
        acceptUntested,
      ),
    );
    appStore.showSuccess(t("admin.plugins.enableSuccess"));
    await loadPlugins();
  } catch (error: unknown) {
    reportSensitiveActionError(error);
  } finally {
    busyID.value = null;
  }
}

async function disablePlugin(plugin: PluginInstallation): Promise<void> {
  if (!window.confirm(t("admin.plugins.confirmDisable"))) return;
  busyID.value = plugin.id;
  try {
    await pluginStepUp.run(() => adminAPI.plugins.disable(plugin.id));
    appStore.showSuccess(t("admin.plugins.disableSuccess"));
    await loadPlugins();
  } catch (error: unknown) {
    reportSensitiveActionError(error);
  } finally {
    busyID.value = null;
  }
}

async function uninstallPlugin(plugin: PluginInstallation): Promise<void> {
  if (!window.confirm(t("admin.plugins.confirmUninstall"))) return;
  busyID.value = plugin.id;
  try {
    await pluginStepUp.run(() => adminAPI.plugins.remove(plugin.id));
    appStore.showSuccess(t("admin.plugins.uninstallSuccess"));
    await loadPlugins();
  } catch (error: unknown) {
    reportSensitiveActionError(error);
  } finally {
    busyID.value = null;
  }
}

async function testPlugin(plugin: PluginInstallation): Promise<void> {
  busyID.value = plugin.id;
  try {
    const result = await pluginStepUp.run(() =>
      adminAPI.plugins.test(plugin.id),
    );
    if (result.success)
      appStore.showSuccess(result.message || t("admin.plugins.testSuccess"));
    else appStore.showError(result.message || t("common.error"));
  } catch (error: unknown) {
    reportSensitiveActionError(error);
  } finally {
    busyID.value = null;
  }
}

async function openConfiguration(plugin: PluginInstallation): Promise<void> {
  configPlugin.value = plugin;
  uiSession.value = null;
  pluginFrameLoaded.value = false;
  clearPendingBridgeRequests();
  uiLoading.value = true;
  uiError.value = "";
  iframeHeight.value = 640;
  try {
    uiSession.value = await adminAPI.plugins.createUISession(plugin.id);
  } catch (error: unknown) {
    uiLoading.value = false;
    uiError.value = errorMessage(error);
  }
}

function closeConfiguration(): void {
  clearPendingBridgeRequests();
  pluginFrameLoaded.value = false;
  configPlugin.value = null;
  uiSession.value = null;
  uiLoading.value = false;
  uiError.value = "";
}

function clearPendingBridgeRequests(): void {
  for (const timeout of pendingBridgeRequests.values()) window.clearTimeout(timeout);
  pendingBridgeRequests.clear();
}

function handlePluginFrameLoad(): void {
  // A load can also be caused by a plugin navigating its iframe. Drop all
  // outstanding responses so a late config response is never sent to the new document.
  if (pluginFrameLoaded.value) clearPendingBridgeRequests();
  pluginFrameLoaded.value = true;
  uiLoading.value = false;
}

function registerBridgeRequest(requestID: string): void {
  const timeout = window.setTimeout(() => {
    pendingBridgeRequests.delete(requestID);
  }, 30_000);
  pendingBridgeRequests.set(requestID, timeout);
}

function postBridgeResult(
  request: PluginBridgeMessage,
  payload: Record<string, unknown>,
): void {
  if (!pluginFrame.value?.contentWindow || !uiSession.value) return;
  const requestID = typeof request.request_id === "string" ? request.request_id.trim() : "";
  const timeout = pendingBridgeRequests.get(requestID);
  if (!requestID || timeout === undefined) return;
  window.clearTimeout(timeout);
  pendingBridgeRequests.delete(requestID);
  pluginFrame.value.contentWindow.postMessage(
    {
      source: "sub2api-plugin-host",
      bridge_token: uiSession.value.bridge_token,
      type: `${request.type}.result`,
      request_id: requestID,
      ...payload,
    },
    // The sandboxed iframe has an opaque origin, so no fixed target origin exists.
    // Pending request tracking plus load invalidation prevents cross-navigation leaks.
    "*",
  );
}

async function handleBridgeMessage(event: MessageEvent): Promise<void> {
  if (
    !uiSession.value ||
    !configPlugin.value ||
    event.source !== pluginFrame.value?.contentWindow ||
    event.origin !== "null"
  )
    return;
  const message = event.data as PluginBridgeMessage;
  if (
    !message ||
    message.source !== "sub2api-plugin-ui" ||
    message.bridge_token !== uiSession.value.bridge_token
  )
    return;

  const requestID = typeof message.request_id === "string" ? message.request_id.trim() : "";
  const expectsResponse =
    message.type === "config.load" ||
    message.type === "config.save" ||
    message.type === "config.test";
  if (expectsResponse) {
    if (!requestID || pendingBridgeRequests.has(requestID)) return;
    registerBridgeRequest(requestID);
  }

  try {
    switch (message.type) {
      case "sub2api.plugin.ready":
        uiLoading.value = false;
        break;
      case "config.load": {
        const config = await adminAPI.plugins.getConfig(configPlugin.value.id);
        postBridgeResult(message, { ok: true, config });
        break;
      }
      case "config.save": {
        if (
          !message.config ||
          typeof message.config !== "object" ||
          Array.isArray(message.config)
        ) {
          throw new Error(t("admin.plugins.bridgeRejected"));
        }
        const config = await pluginStepUp.run(() =>
          adminAPI.plugins.saveConfig(
            configPlugin.value!.id,
            message.config as Record<string, unknown>,
          ),
        );
        postBridgeResult(message, { ok: true, config });
        appStore.showSuccess(t("common.saved"));
        break;
      }
      case "config.test": {
        const result = await pluginStepUp.run(() =>
          adminAPI.plugins.test(configPlugin.value!.id),
        );
        postBridgeResult(message, { ok: result.success, result });
        if (result.success)
          appStore.showSuccess(
            result.message || t("admin.plugins.testSuccess"),
          );
        else appStore.showError(result.message || t("common.error"));
        break;
      }
      case "ui.resize": {
        const height = Number(message.height);
        if (Number.isFinite(height))
          iframeHeight.value = Math.min(960, Math.max(520, Math.round(height)));
        break;
      }
      case "ui.notify": {
        const text =
          typeof message.message === "string"
            ? message.message.slice(0, 500)
            : "";
        if (!text) break;
        if (message.level === "error") appStore.showError(text);
        else if (message.level === "success") appStore.showSuccess(text);
        else appStore.showInfo(text);
        break;
      }
    }
  } catch (error: unknown) {
    if (isStepUpBlocked(error)) reportSensitiveActionError(error);
    postBridgeResult(message, {
      ok: false,
      error: isStepUpCancelled(error) ? t("common.cancel") : errorMessage(error),
    });
  }
}

type BadgeColor = "success" | "error" | "warning" | "neutral";

function stateColor(state: PluginInstallation["state"]): BadgeColor {
  if (state === "enabled") return "success";
  if (state === "error" || state === "incompatible") return "error";
  if (state === "starting") return "warning";
  return "neutral";
}

function compatibilityColor(
  status: PluginInstallation["compatibility"]["status"],
): BadgeColor {
  if (status === "compatible") return "success";
  if (status === "untested") return "warning";
  return "error";
}

onMounted(() => {
  window.addEventListener("message", handleBridgeMessage);
  void loadPlugins();
});

onBeforeUnmount(() => {
  window.removeEventListener("message", handleBridgeMessage);
  clearPendingBridgeRequests();
});
</script>
