# Layout Components

The console frame is the Nuxt UI dashboard (Vue mode), laid out like the official
`nuxt-ui-templates/dashboard-vue` template.

## AppLayout.vue

`UDashboardGroup` > `AppSidebar` + one `UDashboardPanel` (navbar, optional toolbar row, body)
+ `ConsoleNavigationSearch` (`UDashboardSearch`). Every page wraps itself in it:

```vue
<template>
  <AppLayout>
    <!-- page content (goes into the panel body) -->
  </AppLayout>
</template>
```

- The panel body is the scroll container, not the window. `panelScroll.ts` starts a new
  page at the top and restores the position on back / forward. Sticky elements count from
  the top of the body (inside its padding).
- The sakura canvas (`InkSakura`) sits behind the frame and shows in empty areas.
- Root classes `console-signal`, `signal-admin` (admin routes) and `signal-nav-collapsed`
  are kept for page styles.

## AppSidebar.vue

`UDashboardSidebar`, collapsible to a 4rem rail, resizable, a slideover on phones. Its
collapsed / open state is `appStore.sidebarCollapsed` / `appStore.mobileOpen` (persisted by
`UDashboardGroup` under the `sub2api-console` storage key).

- Header: logo, site name and `VersionBadge` (where the template has TeamsMenu).
- `UDashboardSearchButton` (opens the palette; Ctrl/Cmd+K works anywhere).
- Vertical `UNavigationMenu` built from `consoleNav.tsx` (shared with the palette): admins
  get closed groups (概览 / 账号与渠道 / 用户与计费 / 运营 / 监控与诊断 / 安全 / 系统 / 我的账户),
  the group holding the current page opens itself; users get four open groups (概览 /
  API 与用量 / 充值与订单 / 帮助与账户). Feature flags, simple mode, admin / observer / user
  roles and custom menu items (with sanitized SVG icons) apply as before. On the rail a
  group opens its pages in a popover. While the onboarding tour runs, the groups holding
  its anchors stay open.
- An area keeps one entry that points at its top route and stays lit on its sub-pages
  (`match`, a path prefix); detail pages light the entry of their `meta.parent`.
- To add a page: add a `link(path, label)` to its group in `consoleNav.tsx` (it shows in the
  sidebar and the palette); `searchOnly: true` lists it in the palette only (sub-pages that
  the area's `PageSubnav` reaches). Icon names must be literal `i-lucide-*` strings.
- Bottom group: subscription status, model plaza, docs.
- Footer: `UserMenu` (account, profile, API keys, theme, language, GitHub, contact,
  onboarding tour, logout).
- Onboarding anchors are `data-tour` attributes from `SIDEBAR_TOUR_ANCHORS` in
  `components/Guide/steps.ts`.

## AppHeader.vue

`UDashboardNavbar` (collapse button and page title on the left; page actions,
notifications, light / dark and balance on the right) and the `UDashboardToolbar` row,
which appears only while a page puts something in it.

The title is the route's `meta.titleKey` / `meta.title` (custom pages: the menu label),
unless a page header names the page itself.

## Putting page content in the navbar / toolbar

Pages reach the frame through `dashboardFrame.ts` (provide / inject + Teleport targets).
Outside the frame (a spec that stubs `AppLayout`, a public page) everything renders in place.

- `AdminPageHeader` / `UserPageHeader`: `title` (or a `#title` slot) names the page in the
  navbar, `#badge` and a live `#description` status follow the title, `#actions` go to the
  navbar right side from 1280px and to the toolbar row below that. A plain `description`
  prop is not shown in the frame; `#meta` stays in the page.
- `PageNavbar`: any buttons for the navbar right side (`pinned` keeps them there on every
  width).
- `PageToolbar`: a filter row under the navbar; default slot (or `#left`) on the left,
  `#right` on the right.
- `PageSubnav`: an area's sub-pages as a horizontal `UNavigationMenu` (highlight) in that
  toolbar row, like the template's settings page. `items` as for UNavigationMenu
  (`{ label, icon?, to, badge? }`, or two lists: the second sits on the right); the active
  item is the deepest `to` that contains the current route; `label` names the nav for
  screen readers; `#right` puts buttons at the row's end.

```vue
<!-- views/admin/SettingsView.vue: parent route /admin/settings, children general, security, ... -->
<AppLayout>
  <AdminPageHeader :title="t('admin.settings.title')" />
  <PageSubnav :label="t('admin.settings.title')" :items="[
    { label: t('admin.settings.tabs.general'), icon: 'i-lucide-house', to: '/admin/settings/general' },
    { label: t('admin.settings.tabs.security'), icon: 'i-lucide-shield', to: '/admin/settings/security' }
  ]" />
  <RouterView />
</AppLayout>
```

## Breadcrumb (route meta `parent`)

A detail or sub-page names the page it belongs to; the navbar then shows that trail before
the title (a back button on phones), and the sidebar lights the parent's entry.

```ts
{ path: '/admin/accounts/:id', name: 'AdminAccountDetail', component: AccountDetailView,
  meta: { requiresAdmin: true, titleKey: 'admin.accounts.detail', parent: 'AdminAccounts' } }
```

`parent` is a route name (its params are taken from the current route, so
`/admin/groups/:id/pricing` → `parent: 'AdminGroupDetail'` keeps `:id`) or a path
(`parent: '/admin/accounts'`). The parent's own `meta.parent` continues the trail (up to 4).
The crumb label is the parent's `titleKey` / `title`; the current page keeps its title
(the page header's `title` can name the record, e.g. the account name).

```vue
<AppLayout>
  <AdminPageHeader :title="t('admin.usage.title')">
    <template #actions><UButton icon="i-lucide-plus" :label="t('common.create')" /></template>
  </AdminPageHeader>
  <PageToolbar>
    <DateRangePicker v-model:start-date="start" v-model:end-date="end" />
    <template #right><Select v-model="granularity" :options="options" /></template>
  </PageToolbar>
  <!-- body -->
</AppLayout>
```

## TablePageLayout.vue

Fills the panel height on desktop (the table scrolls inside); on phones the panel scrolls.

## AuthLayout.vue

Centered card layout for the sign-in pages (not part of the console frame).

## Styling

Light mode uses Nuxt UI's own tokens (`--ui-*`), so the Appearance settings (primary,
neutral, radius, font) apply. Dark mode maps the plum-charcoal palette onto the same tokens
(`styles/nuxt-ui-theme.css`). Legacy page classes (`.btn`, `.card`, `.input`, tables) follow
the tokens through `style.css` and `styles/console-*.css`.

The panel is transparent so the sakura painting shows in empty areas. Translucent fills
inside it (`bg-elevated/50` subtle cards, soft badges and buttons, table headers) get an
opaque backing in the colour behind them (`styles/console-surfaces.css`), and bare tables a
fill, so the painting never shows through content. Nothing to do per page; just don't put
a translucent veil you want to stay see-through anywhere but on an `absolute` / `fixed`
element.
