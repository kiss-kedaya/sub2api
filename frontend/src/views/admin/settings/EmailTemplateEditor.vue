<template>
  <SettingsSection
    :title="t('admin.settings.emailTemplates.title')"
    :description="t('admin.settings.emailTemplates.description')"
    :loading="loadingList"
  >
    <template #actions>
      <UButton
        color="neutral"
        variant="outline"
        :loading="previewing"
        :disabled="loadingTemplate || previewing || !canPreview"
        @click="refreshPreview"
      >
        {{ previewing ? t("admin.settings.emailTemplates.previewing") : t("admin.settings.emailTemplates.preview") }}
      </UButton>
      <UButton
        color="neutral"
        variant="outline"
        :loading="restoring"
        :disabled="loadingTemplate || restoring || !selectedEvent || !selectedLocale"
        @click="restoreOfficial"
      >
        {{ restoring ? t("admin.settings.emailTemplates.restoring") : t("admin.settings.emailTemplates.restoreOfficial") }}
      </UButton>
      <UButton :loading="saving" :disabled="loadingTemplate || saving || !canSave" @click="saveTemplate">
        {{ saving ? t("admin.settings.emailTemplates.saving") : t("admin.settings.emailTemplates.save") }}
      </UButton>
    </template>

    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <UFormField :label="t('admin.settings.emailTemplates.event')">
        <Select
          id="email-template-event"
          v-model="selectedEvent"
          :options="eventSelectOptions"
          :disabled="loadingTemplate || eventOptions.length === 0"
          class="w-full"
        />
      </UFormField>
      <UFormField :label="t('admin.settings.emailTemplates.locale')">
        <Select
          id="email-template-locale"
          v-model="selectedLocale"
          :options="localeSelectOptions"
          :disabled="loadingTemplate || localeOptions.length === 0"
          class="w-full"
        />
      </UFormField>
    </div>

    <div v-if="selectedEventMeta" class="space-y-1">
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-sm font-medium text-highlighted">{{ selectedEventMeta.label }}</span>
        <UBadge color="neutral" variant="subtle" size="sm">{{ selectedEventMeta.categoryLabel }}</UBadge>
        <UBadge :color="selectedEventMeta.optional ? 'warning' : 'success'" variant="subtle" size="sm">
          {{ selectedEventMeta.optional ? localText("可退订通知", "Optional") : localText("事务邮件", "Transactional") }}
        </UBadge>
      </div>
      <p class="text-sm text-toned">{{ selectedEventMeta.timing }}</p>
      <p v-if="selectedEventDescription" class="text-xs text-muted">{{ selectedEventDescription }}</p>
    </div>

    <UAlert
      v-if="!eventOptions.length || !localeOptions.length"
      color="warning"
      variant="subtle"
      icon="i-lucide-triangle-alert"
      :description="t('admin.settings.emailTemplates.empty')"
    />
    <template v-else>
      <UFormField :label="t('admin.settings.emailTemplates.subject')">
        <UInput
          id="email-template-subject"
          v-model="subject"
          type="text"
          :disabled="loadingTemplate"
          :placeholder="t('admin.settings.emailTemplates.subjectPlaceholder')"
          class="w-full"
        />
      </UFormField>
      <UFormField :label="t('admin.settings.emailTemplates.html')" :description="t('admin.settings.emailTemplates.placeholdersHelp')">
        <div class="mb-2 flex flex-wrap gap-1.5" :aria-label="t('admin.settings.emailTemplates.placeholders')">
          <UButton
            v-for="placeholder in placeholderList"
            :key="placeholder"
            color="neutral"
            variant="subtle"
            size="xs"
            class="font-mono"
            @click="copyPlaceholder(placeholder)"
          >
            {{ placeholder }}
          </UButton>
        </div>
        <UTextarea
          id="email-template-html"
          v-model="html"
          :rows="14"
          :disabled="loadingTemplate"
          :placeholder="t('admin.settings.emailTemplates.htmlPlaceholder')"
          class="w-full font-mono"
        />
      </UFormField>
      <UFormField :help="t('admin.settings.emailTemplates.previewSecurityHint')">
        <template #label>
          <span class="flex items-center gap-2">
            {{ t("admin.settings.emailTemplates.livePreview") }}
            <UBadge v-if="isCustomTemplate" color="primary" variant="subtle" size="sm">{{ t("admin.settings.emailTemplates.customized") }}</UBadge>
          </span>
        </template>
        <template #description>{{ previewSubject || t("admin.settings.emailTemplates.noPreview") }}</template>
        <iframe
          class="h-120 w-full rounded-md bg-white ring ring-default"
          sandbox=""
          :srcdoc="previewHtml"
          :title="t('admin.settings.emailTemplates.livePreview')"
        />
      </UFormField>
    </template>
  </SettingsSection>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { adminAPI } from "@/api";
import type {
  EmailTemplateEventOption,
  EmailTemplateOption,
} from "@/api/admin/settings";
import { useAppStore } from "@/stores";
import { extractApiErrorMessage } from "@/utils/apiError";
import Select from "@/components/common/Select.vue";
import SettingsSection from "./SettingsSection.vue";
import { confirm } from '@/composables/useConfirmation'

const { t, locale } = useI18n();
const appStore = useAppStore();

const fallbackPlaceholders = [
  "{{site_name}}",
  "{{recipient_name}}",
  "{{recipient_email}}",
  "{{verification_code}}",
  "{{expires_in_minutes}}",
  "{{reset_url}}",
  "{{subscription_group}}",
  "{{subscription_days}}",
  "{{expiry_time}}",
  "{{days_remaining}}",
  "{{current_balance}}",
  "{{threshold}}",
  "{{recharge_url}}",
  "{{recharge_amount}}",
  "{{order_id}}",
  "{{unsubscribe_url}}",
  "{{account_id}}",
  "{{account_name}}",
  "{{platform}}",
  "{{quota_dimension}}",
  "{{quota_used}}",
  "{{quota_limit}}",
  "{{quota_remaining}}",
  "{{quota_threshold}}",
  "{{triggered_at}}",
  "{{group_name}}",
  "{{moderation_category}}",
  "{{moderation_score}}",
  "{{violation_count}}",
  "{{ban_threshold}}",
  "{{rule_name}}",
  "{{severity}}",
  "{{alert_status}}",
  "{{metric_type}}",
  "{{operator}}",
  "{{metric_value}}",
  "{{threshold_value}}",
  "{{alert_description}}",
  "{{report_name}}",
  "{{report_type}}",
  "{{report_start_time}}",
  "{{report_end_time}}",
  "{{report_summary_display}}",
  "{{report_detail_display}}",
  "{{report_total_requests}}",
  "{{report_success_count}}",
  "{{report_sla_error_count}}",
  "{{report_business_limited_count}}",
  "{{report_sla}}",
  "{{report_error_rate}}",
  "{{report_upstream_error_rate}}",
  "{{report_upstream_error_count_excl_429_529}}",
  "{{report_upstream_429_count}}",
  "{{report_upstream_529_count}}",
  "{{report_latency_p50}}",
  "{{report_latency_p99}}",
  "{{report_ttft_p50}}",
  "{{report_ttft_p99}}",
  "{{report_tokens}}",
  "{{report_qps_current}}",
  "{{report_qps_peak}}",
  "{{report_qps_avg}}",
  "{{report_tps_current}}",
  "{{report_tps_peak}}",
  "{{report_tps_avg}}",
  "{{report_html}}",
];

const loadingList = ref(true);
const loadingTemplate = ref(false);
const saving = ref(false);
const previewing = ref(false);
const restoring = ref(false);
const eventOptions = ref<EmailTemplateOption[]>([]);
const localeOptions = ref<string[]>([]);
const selectedEvent = ref("");
const selectedLocale = ref("");
const subject = ref("");
const html = ref("");
const isCustomTemplate = ref(false);
const placeholders = ref<string[]>([]);
const previewSubject = ref("");
const previewHtml = ref("");
const initializingSelection = ref(false);

interface EventDisplayMeta {
  label: string;
  timing: string;
  categoryLabel: string;
}

function localText(zh: string, en: string): string {
  return locale.value.toLowerCase().startsWith("zh") ? zh : en;
}

const eventDisplayMeta: Record<string, EventDisplayMeta> = {
  "ticket.reply": {
    label: "工单回复通知",
    timing: "客服回复成功后发送给工单所属用户，包含截至该回复的完整对话记录。",
    categoryLabel: "工单",
  },
  "auth.verify_code": {
    label: "邮箱验证码",
    timing: "注册、绑定邮箱、OAuth 补全邮箱或 TOTP 邮箱校验时发送。",
    categoryLabel: "认证安全",
  },
  "auth.password_reset": {
    label: "密码重置",
    timing: "用户请求密码重置链接时发送。",
    categoryLabel: "认证安全",
  },
  "notification_email.verify_code": {
    label: "通知邮箱验证码",
    timing: "用户添加并验证额外通知邮箱时发送。",
    categoryLabel: "认证安全",
  },
  "subscription.purchase_success": {
    label: "订阅开通成功",
    timing: "订阅订单完成支付并成功开通或续期后发送。",
    categoryLabel: "订阅",
  },
  "subscription.expiry_reminder": {
    label: "订阅到期提醒",
    timing: "后台任务在订阅仍有效且距离到期剩余 7 天、3 天、1 天时各发送一次，可通过邮件设置中的开关关闭。",
    categoryLabel: "订阅",
  },
  "balance.low": {
    label: "余额不足提醒",
    timing: "用户余额低于全局或个人配置的提醒阈值时发送。",
    categoryLabel: "计费",
  },
  "balance.recharge_success": {
    label: "余额充值成功",
    timing: "余额充值订单支付完成并入账后发送。",
    categoryLabel: "计费",
  },
  "account.quota_alert": {
    label: "账号限额告警",
    timing: "上游账号的用量达到配置的额度告警阈值时发送给管理员通知邮箱。",
    categoryLabel: "管理告警",
  },
  "content_moderation.violation_notice": {
    label: "内容审计违规提醒",
    timing: "用户请求命中内容审计或风控规则、但尚未被禁用时发送。",
    categoryLabel: "风控",
  },
  "content_moderation.account_disabled": {
    label: "内容审计禁用账号",
    timing: "内容审计违规次数达到封禁阈值并自动禁用用户账号时发送。",
    categoryLabel: "风控",
  },
  "ops.alert": {
    label: "运维告警",
    timing: "运维监控规则触发告警并满足邮件通知配置时发送给运维收件人。",
    categoryLabel: "运维",
  },
  "ops.scheduled_report": {
    label: "运维定时报表",
    timing: "运维日报、周报、错误摘要或账号健康报表到达配置的发送时间时发送；日报和周报的完整指标均可在模板中编辑。",
    categoryLabel: "运维",
  },
};

const eventDisplayMetaEn: Record<string, EventDisplayMeta> = {
  "ticket.reply": {
    label: "Ticket Reply",
    timing: "Sent to the ticket owner after a support reply, including the complete conversation up to that reply.",
    categoryLabel: "Support",
  },
  "auth.verify_code": {
    label: "Email Verification Code",
    timing: "Sent for registration, email binding, OAuth pending email completion, or TOTP email verification.",
    categoryLabel: "Auth",
  },
  "auth.password_reset": {
    label: "Password Reset",
    timing: "Sent when a user requests a password reset link.",
    categoryLabel: "Auth",
  },
  "notification_email.verify_code": {
    label: "Notification Email Verification",
    timing: "Sent when a user adds and verifies an extra notification email address.",
    categoryLabel: "Auth",
  },
  "subscription.purchase_success": {
    label: "Subscription Activated",
    timing: "Sent after a subscription order is paid and the subscription is activated or extended.",
    categoryLabel: "Subscription",
  },
  "subscription.expiry_reminder": {
    label: "Subscription Expiry Reminder",
    timing: "Sent by the background job when an active subscription has 7, 3, or 1 day remaining. It can be disabled in Email settings.",
    categoryLabel: "Subscription",
  },
  "balance.low": {
    label: "Low Balance Alert",
    timing: "Sent when a user's balance drops below the global or personal reminder threshold.",
    categoryLabel: "Billing",
  },
  "balance.recharge_success": {
    label: "Balance Recharge Success",
    timing: "Sent after a balance recharge order is paid and credited.",
    categoryLabel: "Billing",
  },
  "account.quota_alert": {
    label: "Account Quota Alert",
    timing: "Sent to admin notification emails when an upstream account reaches the configured quota alert threshold.",
    categoryLabel: "Admin",
  },
  "content_moderation.violation_notice": {
    label: "Risk Control Violation Notice",
    timing: "Sent when a user request triggers content moderation or risk-control rules but the account is not disabled yet.",
    categoryLabel: "Risk Control",
  },
  "content_moderation.account_disabled": {
    label: "Risk Control Account Disabled",
    timing: "Sent when content moderation reaches the ban threshold and automatically disables the user account.",
    categoryLabel: "Risk Control",
  },
  "ops.alert": {
    label: "Ops Alert",
    timing: "Sent to ops recipients when an ops monitoring rule fires and email notification settings allow it.",
    categoryLabel: "Ops",
  },
  "ops.scheduled_report": {
    label: "Ops Scheduled Report",
    timing: "Sent when a configured daily, weekly, error digest, or account health report reaches its scheduled send time. Every daily and weekly summary metric is editable in this template.",
    categoryLabel: "Ops",
  },
};

function normalizeEventOption(option: EmailTemplateEventOption): EmailTemplateOption {
  if (typeof option === "string") {
    return { value: option };
  }
  return option;
}

function eventMetaFor(option?: EmailTemplateOption | null) {
  if (!option) return null;
  const displayMeta = (
    locale.value.toLowerCase().startsWith("zh")
      ? eventDisplayMeta
      : eventDisplayMetaEn
  )[option.value];
  const label = displayMeta?.label || option.label || option.value;
  const timing = displayMeta?.timing || option.description || "";
  const categoryLabel =
    displayMeta?.categoryLabel || formatCategory(option.category || "");
  return {
    label,
    timing,
    categoryLabel,
    optional: option.optional === true,
  };
}

function formatEventOptionLabel(option: EmailTemplateOption): string {
  const meta = eventMetaFor(option);
  if (!meta) return option.label || option.value;
  return meta.label;
}

function formatCategory(category: string): string {
  const normalized = category.trim().toLowerCase();
  if (!normalized) return localText("通知", "Notification");
  const labels: Record<string, { zh: string; en: string }> = {
    auth: { zh: "认证安全", en: "Auth" },
    subscription: { zh: "订阅", en: "Subscription" },
    billing: { zh: "计费", en: "Billing" },
    admin: { zh: "管理告警", en: "Admin" },
    risk_control: { zh: "风控", en: "Risk Control" },
    ops: { zh: "运维", en: "Ops" },
  };
  const item = labels[normalized];
  return item ? localText(item.zh, item.en) : category;
}

const selectedEventOption = computed(() => {
  return (
    eventOptions.value.find((option) => option.value === selectedEvent.value) ||
    null
  );
});

const selectedEventMeta = computed(() => eventMetaFor(selectedEventOption.value));

const eventSelectOptions = computed(() =>
  eventOptions.value.map((option) => ({ value: option.value, label: formatEventOptionLabel(option) })),
);
const localeSelectOptions = computed(() =>
  localeOptions.value.map((value) => ({ value, label: formatLocale(value) })),
);

const selectedEventDescription = computed(() => {
  return (
    selectedEventOption.value?.description || ""
  );
});

const placeholderList = computed(() => {
  const combined = placeholders.value.length
    ? placeholders.value
    : fallbackPlaceholders;
  return Array.from(
    new Set(
      combined
        .map((item) => formatPlaceholder(item))
        .filter((item) => item.length > 0),
    ),
  );
});

function formatPlaceholder(placeholder: string): string {
  const trimmed = placeholder.trim();
  if (!trimmed) return "";
  if (trimmed.startsWith("{{") && trimmed.endsWith("}}")) return trimmed;
  return `{{${trimmed}}}`;
}

const canSave = computed(
  () =>
    Boolean(selectedEvent.value && selectedLocale.value) &&
    subject.value.trim().length > 0 &&
    html.value.trim().length > 0,
);

const canPreview = computed(
  () => Boolean(selectedEvent.value && selectedLocale.value) && html.value.trim().length > 0,
);

function formatLocale(locale: string): string {
  const lower = locale.toLowerCase();
  if (lower === "zh" || lower.startsWith("zh-")) {
    return t("admin.settings.emailTemplates.localeZh");
  }
  if (lower === "en" || lower.startsWith("en-")) {
    return t("admin.settings.emailTemplates.localeEn");
  }
  return locale;
}

function selectInitialLocale(locales: string[]): string {
  const currentLocale = locale.value.toLowerCase();
  const exactMatch = locales.find(
    (availableLocale) => availableLocale.toLowerCase() === currentLocale,
  );
  if (exactMatch) return exactMatch;

  const currentLanguage = currentLocale.split("-")[0];
  const languageMatch = locales.find(
    (availableLocale) => availableLocale.toLowerCase().split("-")[0] === currentLanguage,
  );
  if (languageMatch) return languageMatch;

  return locales[0] || "";
}

function applyTemplate(template: {
  subject: string;
  html: string;
  is_custom?: boolean;
  placeholders?: string[];
}) {
  subject.value = template.subject;
  html.value = template.html;
  isCustomTemplate.value = template.is_custom === true;
  placeholders.value = template.placeholders || [];
}

async function loadTemplate() {
  if (!selectedEvent.value || !selectedLocale.value) return;
  loadingTemplate.value = true;
  try {
    const template = await adminAPI.settings.getEmailTemplate(
      selectedEvent.value,
      selectedLocale.value,
    );
    applyTemplate(template);
    await refreshPreview();
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  } finally {
    loadingTemplate.value = false;
  }
}

async function loadTemplateList() {
  loadingList.value = true;
  try {
    const response = await adminAPI.settings.getEmailTemplates();
    eventOptions.value = response.events.map(normalizeEventOption);
    localeOptions.value = response.locales;
    placeholders.value = response.placeholders || [];
    initializingSelection.value = true;
    selectedEvent.value = eventOptions.value[0]?.value || "";
    selectedLocale.value = selectInitialLocale(response.locales);
    await loadTemplate();
    initializingSelection.value = false;
  } catch (err: unknown) {
    initializingSelection.value = false;
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  } finally {
    loadingList.value = false;
  }
}

async function saveTemplate() {
  if (!canSave.value) {
    appStore.showError(t("admin.settings.emailTemplates.validationRequired"));
    return;
  }
  saving.value = true;
  try {
    const template = await adminAPI.settings.updateEmailTemplate(
      selectedEvent.value,
      selectedLocale.value,
      {
        subject: subject.value,
        html: html.value,
      },
    );
    applyTemplate(template);
    await refreshPreview();
    appStore.showSuccess(t("admin.settings.emailTemplates.saveSuccess"));
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  } finally {
    saving.value = false;
  }
}

async function refreshPreview() {
  if (!canPreview.value) {
    previewSubject.value = "";
    previewHtml.value = "";
    return;
  }
  previewing.value = true;
  try {
    const preview = await adminAPI.settings.previewEmailTemplate({
      event: selectedEvent.value,
      locale: selectedLocale.value,
      subject: subject.value,
      html: html.value,
    });
    previewSubject.value = preview.subject;
    previewHtml.value = preview.html;
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  } finally {
    previewing.value = false;
  }
}

async function restoreOfficial() {
  if (!selectedEvent.value || !selectedLocale.value) return;
  if (!(await confirm(t("admin.settings.emailTemplates.restoreConfirm")))) return;

  restoring.value = true;
  try {
    const template = await adminAPI.settings.restoreOfficialEmailTemplate(
      selectedEvent.value,
      selectedLocale.value,
    );
    applyTemplate(template);
    await refreshPreview();
    appStore.showSuccess(t("admin.settings.emailTemplates.restoreSuccess"));
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  } finally {
    restoring.value = false;
  }
}

async function copyPlaceholder(placeholder: string) {
  try {
    await navigator.clipboard.writeText(placeholder);
    appStore.showSuccess(t("admin.settings.emailTemplates.placeholderCopied"));
  } catch {
    appStore.showError(t("common.error"));
  }
}

watch([selectedEvent, selectedLocale], ([eventValue, localeValue], [oldEvent, oldLocale]) => {
  if (initializingSelection.value) return;
  if (!eventValue || !localeValue) return;
  if (eventValue === oldEvent && localeValue === oldLocale) return;
  void loadTemplate();
});

onMounted(() => {
  void loadTemplateList();
});
</script>
