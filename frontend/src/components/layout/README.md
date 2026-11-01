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
- Vertical `UNavigationMenu` built from the nav declarations in the file (items, groups,
  tier headers, feature flags, simple mode, admin / observer / user roles, custom menu items
  with sanitized SVG icons). On the rail a group opens its children in a popover.
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
