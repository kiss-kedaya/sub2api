import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { computed, defineComponent, h, provide, ref } from "vue";
import { portalTargetInjectionKey } from "@nuxt/ui/composables/usePortal";

import CodexManifestAccountsField from "../CodexManifestAccountsField.vue";
import { adminAPI } from "@/api/admin";
import type { CodexModelsManifestConfig } from "@/types";

vi.mock("vue-i18n", async () => {
  const actual = await vi.importActual<typeof import("vue-i18n")>("vue-i18n");
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  };
});

vi.mock("@/api/admin", () => ({
  adminAPI: {
    accounts: {
      list: vi.fn(),
    },
  },
}));

const disabledConfig = (): CodexModelsManifestConfig => ({
  enabled: false,
  account_ids: [],
  fallback_to_scheduler: false,
});

const enabledConfig = (): CodexModelsManifestConfig => ({
  enabled: true,
  account_ids: [],
  fallback_to_scheduler: false,
});

// 挂载一个 v-model 双向绑定的宿主，验证完整的选择→移除流程。
const mountInteractive = (initial: CodexModelsManifestConfig, accountNames?: Record<number, string>) => {
  const host = defineComponent({
    setup() {
      const config = ref(initial);
      return () =>
        h(CodexManifestAccountsField, {
          groupId: 7,
          modelValue: config.value,
          accountNames,
          "onUpdate:modelValue": (value: CodexModelsManifestConfig) => {
            config.value = value;
          },
        });
    },
  });
  return mount(host, {
    global: {
      stubs: {
        Icon: { props: ["name"], template: '<i :data-icon="name" />' },
        Teleport: true,
      },
    },
  });
};

const mountField = (modelValue: CodexModelsManifestConfig, accountNames?: Record<number, string>) =>
  mount(CodexManifestAccountsField, {
    props: {
      groupId: 7,
      modelValue,
      ...(accountNames ? { accountNames } : {}),
    },
    global: {
      stubs: {
        Icon: { props: ["name"], template: '<i :data-icon="name" />' },
        Teleport: true,
      },
    },
  });

describe("CodexManifestAccountsField", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it("hides account controls while disabled and reveals them after enabling", async () => {
    const wrapper = mountField(disabledConfig());

    expect(wrapper.find('[data-testid="codex-manifest-search"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="codex-manifest-fallback-toggle"]').exists()).toBe(false);
    expect(wrapper.text()).toContain("admin.groups.codexModelsManifest.disabledHint");

    await wrapper.find('[data-testid="codex-manifest-toggle"]').trigger("click");

    const emitted = wrapper.emitted("update:modelValue");
    expect(emitted).toHaveLength(1);
    expect(emitted?.[0]?.[0]).toMatchObject({ enabled: true });

    const enabled = mountField(enabledConfig());
    expect(enabled.find('[data-testid="codex-manifest-search"]').exists()).toBe(true);
    expect(enabled.find('[data-testid="codex-manifest-fallback-toggle"]').exists()).toBe(true);
    expect(enabled.text()).toContain("admin.groups.codexModelsManifest.enabledHint");
  });

  it("selects accounts from search results and removes them via tags", async () => {
    vi.mocked(adminAPI.accounts.list).mockResolvedValue({
      items: [
        { id: 5, name: "oauth-five" },
        { id: 6, name: "apikey-six" },
      ],
    } as never);
    const wrapper = mountInteractive(
      { ...enabledConfig(), account_ids: [5] },
      { 5: "oauth-five" },
    );

    const initialTags = wrapper.find('[data-testid="codex-manifest-selected-tags"]');
    expect(initialTags.text()).toContain("oauth-five");

    const search = wrapper.find('[data-testid="codex-manifest-search"]');
    await search.trigger("focus");
    await search.setValue("oauth");
    vi.advanceTimersByTime(300);
    await flushPromises();

    expect(adminAPI.accounts.list).toHaveBeenCalledWith(
      1,
      20,
      { search: "oauth", platform: "openai", group: "7" },
      expect.anything(),
    );

    const dropdown = wrapper.find('[data-testid="codex-manifest-dropdown"]');
    expect(dropdown.exists()).toBe(true);
    expect(dropdown.text()).toContain("apikey-six");

    // 账号 6 未选中：点击加入。
    const option = dropdown.findAll("button").find((b) => b.text().includes("apikey-six"));
    await option!.trigger("click");
    await flushPromises();

    expect(wrapper.text()).toContain("apikey-six", "新选账号立即以名称展示");

    // 移除账号 5：点击其标签上的删除按钮。
    const removeButton = wrapper
      .find('[data-testid="codex-manifest-selected-tags"]')
      .findAll("button")
      .find((b) => b.attributes("aria-label") === "remove account 5");
    await removeButton!.trigger("click");
    await flushPromises();

    const tags = wrapper.find('[data-testid="codex-manifest-selected-tags"]');
    expect(tags.text()).toContain("apikey-six");
    expect(tags.text()).not.toContain("oauth-five");
  });

  it("fails validation when enabled without accounts", async () => {
    const wrapper = mountField(enabledConfig());

    expect(wrapper.vm.validate()).toBe(false);
    await flushPromises();
    expect(
      wrapper.find('[data-testid="codex-manifest-validation-error"]').text(),
    ).toBe("admin.groups.codexModelsManifest.selectAtLeastOne");

    // GroupsView 关闭对话框时通过可选链调用 resetValidation，必须真实存在且生效。
    expect(typeof wrapper.vm.resetValidation).toBe("function");
    wrapper.vm.resetValidation();
    await flushPromises();
    expect(
      wrapper.find('[data-testid="codex-manifest-validation-error"]').exists(),
    ).toBe(false);

    await wrapper.setProps({
      modelValue: { ...enabledConfig(), account_ids: [9] },
    });
    expect(wrapper.vm.validate()).toBe(true);
    await flushPromises();
    expect(
      wrapper.find('[data-testid="codex-manifest-validation-error"]').exists(),
    ).toBe(false);
  });

  it("closes the dropdown on outside click and shows the empty state", async () => {
    vi.mocked(adminAPI.accounts.list).mockResolvedValue({ items: [] } as never);
    const wrapper = mountField(enabledConfig());

    const search = wrapper.find('[data-testid="codex-manifest-search"]');
    await search.trigger("focus");
    await search.setValue("nonexistent");
    vi.advanceTimersByTime(300);
    await flushPromises();

    // 有关键词但无结果：展示空状态提示而非静默无反应。
    const dropdown = wrapper.find('[data-testid="codex-manifest-dropdown"]');
    expect(dropdown.exists()).toBe(true);
    expect(wrapper.find('[data-testid="codex-manifest-search-empty"]').text()).toBe(
      "admin.groups.codexModelsManifest.searchEmpty",
    );

    // 点击组件外部：收起下拉。
    document.body.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await flushPromises();
    expect(wrapper.find('[data-testid="codex-manifest-dropdown"]').exists()).toBe(false);

    // 点击输入框内部：不收起。
    await search.trigger("click");
    await search.trigger("focus");
    expect(wrapper.find('[data-testid="codex-manifest-dropdown"]').exists()).toBe(true);
    wrapper.unmount();
  });

  describe("conditional Teleport and viewport positioning (non-stubbed Teleport)", () => {
    it("renders dropdown inline when no portalTarget or clipping ancestor exists", async () => {
      vi.mocked(adminAPI.accounts.list).mockResolvedValue({
        items: [{ id: 11, name: "inline-acc" }],
      } as never);

      const wrapper = mount(CodexManifestAccountsField, {
        attachTo: document.body,
        props: {
          groupId: 7,
          modelValue: enabledConfig(),
        },
        global: {
          stubs: {
            Icon: { props: ["name"], template: '<i :data-icon="name" />' },
          },
        },
      });

      await wrapper.find('[data-testid="codex-manifest-search"]').trigger("focus");
      vi.advanceTimersByTime(300);
      await flushPromises();

      const inlineDropdown = wrapper.find('[data-testid="codex-manifest-dropdown"]');
      expect(inlineDropdown.exists()).toBe(true);
      expect(inlineDropdown.classes()).toContain("rounded-xl");
      expect(inlineDropdown.text()).toContain("inline-acc");
      wrapper.unmount();
    });

    it("teleports dropdown into portalTargetInjectionKey host when provided", async () => {
      vi.mocked(adminAPI.accounts.list).mockResolvedValue({
        items: [{ id: 12, name: "dialog-portal-acc" }],
      } as never);

      const PortalHost = defineComponent({
        setup() {
          const portalEl = ref<HTMLElement | null>(null);
          provide(
            portalTargetInjectionKey,
            computed(() => portalEl.value ?? "body"),
          );
          return () =>
            h("div", [
              h(CodexManifestAccountsField, {
                groupId: 7,
                modelValue: enabledConfig(),
              }),
              h("div", { ref: portalEl, "data-testid": "custom-portal-host" }),
            ]);
        },
      });

      const wrapper = mount(PortalHost, {
        attachTo: document.body,
        global: {
          stubs: {
            Icon: { props: ["name"], template: '<i :data-icon="name" />' },
          },
        },
      });

      await wrapper.find('[data-testid="codex-manifest-search"]').trigger("focus");
      vi.advanceTimersByTime(300);
      await flushPromises();

      const portalHost = wrapper.find('[data-testid="custom-portal-host"]');
      const dropdownInPortal = portalHost.find('[data-testid="codex-manifest-dropdown"]');
      expect(dropdownInPortal.exists()).toBe(true);
      expect(dropdownInPortal.text()).toContain("dialog-portal-acc");
      wrapper.unmount();
    });

    it("teleports dropdown to document.body when an ancestor clips overflow", async () => {
      vi.mocked(adminAPI.accounts.list).mockResolvedValue({
        items: [{ id: 13, name: "clipped-acc" }],
      } as never);

      const ClippedHost = defineComponent({
        setup() {
          return () =>
            h("div", { style: { overflow: "hidden" }, "data-testid": "clip-container" }, [
              h(CodexManifestAccountsField, {
                groupId: 7,
                modelValue: enabledConfig(),
              }),
            ]);
        },
      });

      const wrapper = mount(ClippedHost, {
        attachTo: document.body,
        global: {
          stubs: {
            Icon: { props: ["name"], template: '<i :data-icon="name" />' },
          },
        },
      });

      await wrapper.find('[data-testid="codex-manifest-search"]').trigger("focus");
      vi.advanceTimersByTime(300);
      await flushPromises();

      // Should NOT remain inside the overflow:hidden container; should be teleported to body.
      expect(
        wrapper.find('[data-testid="clip-container"] [data-testid="codex-manifest-dropdown"]').exists(),
      ).toBe(false);
      const bodyDropdown = document.body.querySelector('[data-testid="codex-manifest-dropdown"]');
      expect(bodyDropdown).not.toBeNull();
      expect(bodyDropdown?.textContent).toContain("clipped-acc");
      wrapper.unmount();
    });

    it("flips dropdown above the input with bottom anchoring and maxHeight clamping near bottom edge", async () => {
      vi.mocked(adminAPI.accounts.list).mockResolvedValue({
        items: [{ id: 14, name: "bottom-edge-acc" }],
      } as never);

      const originalInnerHeight = window.innerHeight;
      Object.defineProperty(window, "innerHeight", { value: 600, configurable: true });
      const rectSpy = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => ({
        top: 540,
        bottom: 580,
        left: 24,
        right: 344,
        width: 320,
        height: 40,
        x: 24,
        y: 540,
        toJSON: () => ({}),
      }));

      const wrapper = mount(CodexManifestAccountsField, {
        attachTo: document.body,
        props: {
          groupId: 7,
          modelValue: enabledConfig(),
        },
        global: {
          stubs: {
            Icon: { props: ["name"], template: '<i :data-icon="name" />' },
          },
        },
      });

      await wrapper.find('[data-testid="codex-manifest-search"]').trigger("focus");
      vi.advanceTimersByTime(300);
      await flushPromises();

      const dropdown = wrapper.get('[data-testid="codex-manifest-dropdown"]');
      const style = dropdown.attributes("style") ?? "";
      expect(style).toContain("top: auto");
      expect(style).toContain("bottom: 64px");
      expect(style).toContain("max-height: 192px");
      expect(style).toContain("transform-origin: bottom left");

      rectSpy.mockRestore();
      Object.defineProperty(window, "innerHeight", { value: originalInnerHeight, configurable: true });
      wrapper.unmount();
    });
  });
});
