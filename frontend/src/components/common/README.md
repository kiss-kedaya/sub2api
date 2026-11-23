# Common Components

This directory contains reusable Vue 3 components built with Composition API, TypeScript, and TailwindCSS.

## Components

### DataTable.vue

A generic data table component with sorting, loading states, and custom cell rendering.

**Props:**

- `columns: Column[]` - Array of column definitions with key, label, sortable, and formatter
- `data: any[]` - Array of data objects to display
- `loading?: boolean` - Show loading skeleton
- `defaultSortKey?: string` - Default sort key (only used if no persisted sort state)
- `defaultSortOrder?: 'asc' | 'desc'` - Default sort order (default: `asc`)
- `sortStorageKey?: string` - Persist sort state (key + order) to localStorage
- `rowKey?: string | (row: any) => string | number` - Row key field or resolver (defaults to `row.id`, falls back to index)

**Slots:**

- `empty` - Custom empty state content
- `cell-{key}` - Custom cell renderer for specific column (receives `row` and `value`)

**Usage:**

```vue
<DataTable
  :columns="[
    { key: 'name', label: 'Name', sortable: true },
    { key: 'email', label: 'Email' },
    { key: 'status', label: 'Status', formatter: (val) => val.toUpperCase() }
  ]"
  :data="users"
  :loading="isLoading"
>
  <template #cell-actions="{ row }">
    <UButton color="neutral" variant="ghost" icon="i-lucide-pencil" aria-label="Edit" @click="editUser(row)" />
  </template>
</DataTable>
```

---

### Pagination.vue

Pagination component with page numbers, navigation, and page size selector.

**Props:**

- `total: number` - Total number of items
- `page: number` - Current page (1-indexed)
- `pageSize: number` - Items per page
- `pageSizeOptions?: number[]` - Available page size options (default: [10, 20, 50, 100])

**Events:**

- `update:page` - Emitted when page changes
- `update:pageSize` - Emitted when page size changes

**Usage:**

```vue
<Pagination
  :total="totalUsers"
  :page="currentPage"
  :pageSize="pageSize"
  @update:page="currentPage = $event"
  @update:pageSize="pageSize = $event"
/>
```

---

### Modal.vue

Modal dialog with customizable size and close behavior.

**Props:**

- `show: boolean` - Control modal visibility
- `title: string` - Modal title
- `size?: 'sm' | 'md' | 'lg' | 'xl' | 'full'` - Modal size (default: 'md')
- `closeOnEscape?: boolean` - Close on Escape key (default: true)
- `closeOnClickOutside?: boolean` - Close on backdrop click (default: true)

**Events:**

- `close` - Emitted when modal should close

**Slots:**

- `default` - Modal body content
- `footer` - Modal footer content

**Usage:**

```vue
<Modal :show="showModal" title="Edit User" size="lg" @close="showModal = false">
  <form @submit.prevent="saveUser">
    <!-- Form content -->
  </form>

  <template #footer>
    <UButton color="neutral" variant="subtle" label="Cancel" @click="showModal = false" />
    <UButton label="Save" @click="saveUser" />
  </template>
</Modal>
```

---

### ConfirmDialog.vue

Confirmation dialog built on top of Modal component.

**Props:**

- `show: boolean` - Control dialog visibility
- `title: string` - Dialog title
- `message: string` - Confirmation message
- `confirmText?: string` - Confirm button text (default: 'Confirm')
- `cancelText?: string` - Cancel button text (default: 'Cancel')
- `danger?: boolean` - Use danger/red styling (default: false)

**Events:**

- `confirm` - Emitted when user confirms
- `cancel` - Emitted when user cancels

**Usage:**

```vue
<ConfirmDialog
  :show="showDeleteConfirm"
  title="Delete User"
  message="Are you sure you want to delete this user? This action cannot be undone."
  confirm-text="Delete"
  cancel-text="Cancel"
  danger
  @confirm="deleteUser"
  @cancel="showDeleteConfirm = false"
/>
```

---

### StatCard.vue

Statistics card component for displaying metrics with optional change indicators.

**Props:**

- `title: string` - Card title
- `value: number | string` - Main value to display
- `icon?: Component` - Icon component
- `change?: number` - Percentage change value
- `changeType?: 'up' | 'down' | 'neutral'` - Change direction (default: 'neutral')
- `formatValue?: (value) => string` - Custom value formatter

**Usage:**

```vue
<StatCard title="Total Users" :value="1234" :icon="UserIcon" :change="12.5" change-type="up" />
```

---

### Toast.vue

Toast notification component that automatically displays toasts from the app store.

**Usage:**

```vue
<!-- Add once in App.vue or layout -->
<Toast />
```

```typescript
// Trigger toasts from anywhere using the app store
import { useAppStore } from '@/stores/app'

const appStore = useAppStore()

appStore.addToast({
  type: 'success',
  title: 'Success!',
  message: 'User created successfully',
  duration: 3000
})

appStore.addToast({
  type: 'error',
  message: 'Failed to delete user'
})
```

---

### LoadingSpinner.vue

Simple animated loading spinner.

**Props:**

- `size?: 'sm' | 'md' | 'lg' | 'xl'` - Spinner size (default: 'md')
- `color?: 'primary' | 'secondary' | 'white' | 'gray'` - Spinner color (default: 'primary')

**Usage:**

```vue
<LoadingSpinner size="lg" color="primary" />
```

---

### EmptyState.vue

Empty state placeholder with icon, message, and optional action button.

**Props:**

- `icon?: Component` - Icon component
- `title: string` - Empty state title
- `description: string` - Empty state description
- `actionText?: string` - Action button text
- `actionTo?: string | object` - Router link destination
- `actionIcon?: boolean` - Show plus icon in button (default: true)

**Slots:**

- `icon` - Custom icon content
- `action` - Custom action button/link

**Usage:**

```vue
<EmptyState
  title="No users found"
  description="Get started by creating your first user account."
  action-text="Add User"
  :action-to="{ name: 'users-create' }"
/>
```

## Shared record / auth patterns

Three patterns were extracted because the same markup and colour decisions had
been copied into several pages. Reach for these before writing a new list, code
field or OAuth button. All of them read the Nuxt UI theme tokens, so a light /
dark switch needs no per-page CSS.

### TransactionListCard.vue + TransactionList.vue

The "recent records" card: title + subtitle, one row per record (round icon
plate, title/subtitle, signed amount on the right), hairline dividers, empty
state, loading skeleton. `TransactionList` is the bare list for dialogs.

Row model (`transactionList.ts`): `{ id, title, subtitle?, icon?, type?,
amount?, amountPrefix?, amountSuffix?, amountSign?, tone?, status?, meta?,
testId? }`. The tone decides both the sign and the colour:

| tone       | amount        | meaning                                   |
| ---------- | ------------- | ----------------------------------------- |
| `positive` | green, `+$X`  | money arrived                             |
| `negative` | ink, `-$X`    | money left the account                    |
| `reversal` | rose, `-$X`   | an earlier credit was rolled back         |
| `pending`  | muted, no sign| not settled yet                           |
| `neutral`  | muted, no sign| no balance effect (e.g. day counts)       |

Props: `title`, `subtitle`, `items`, `loading`, `skeletonRows`, `emptyTitle`,
`emptyDescription`, `emptyTestId`, `emptyIcon`, `loadingLabel`, `testId`,
`interactive` (rows clickable → `select` event).
Slots: `actions` (header right), `toolbar` (above rows), `item-details`
(per-row extra lines), `empty-actions`, `footer`.

```vue
<TransactionListCard
  :title="t('deposit.history')"
  :subtitle="t('deposit.autoRefresh')"
  :items="historyItems"
  :loading="loading"
  :empty-title="t('deposit.noHistory')"
  @empty-actions="resetFilters"
>
  <template #actions><UButton :label="t('common.refresh')" @click="refresh" /></template>
  <template #item-details="{ item }"><span>{{ item.row.revoke_reason }}</span></template>
</TransactionListCard>
```

### SocialLoginButtons.vue

Provider-list driven sign-in buttons: full-width, quiet raised surface, left
brand mark, centred label, 10px gaps, one shared radius. Multi-colour marks are
passed through the dynamic `#icon-<id>` slot; a single-glyph brand can just set
`icon` to a UIcon name.

Props: `providers: { id, label, icon?, ariaLabel?, disabled?, testId? }[]`,
`disabled`, `divider`, `dividerLabel`, `columns: 1 | 2`.
Events: `select` (provider id). Slots: `#icon-<provider id>`, `footer`.

```vue
<SocialLoginButtons :providers="providers" divider :divider-label="t('auth.oauthOrContinue')" @select="startLogin">
  <template #icon-google><GoogleMark class="size-5" /></template>
</SocialLoginButtons>
```

### VerificationCodeInput.vue (auth)

Segmented one-time-code field: one input per digit, optional dot between the
halves, digits only, auto-advance, Backspace walk-back, whole-code paste /
iOS autofill distribution, `complete` on completion and `submit` when
`autoSubmit` is on (emitted only on the transition to a complete code).
The page keeps send/verify/countdown logic in the `footer` slot.

Props: `modelValue`, `length`, `separator`, `separatorAfter`, `autoSubmit`,
`disabled`, `invalid`, `label`, `hint`, `errorText`, `autocomplete`, `inputId`,
`testId`. Events: `update:modelValue`, `complete`, `submit`.

```vue
<VerificationCodeInput
  v-model="code"
  input-id="code"
  :label="t('auth.verificationCode')"
  :auto-submit="canAutoSubmit"
  @submit="handleVerify"
>
  <template #footer><UButton variant="link" :label="t('auth.resendCode')" @click="resend" /></template>
</VerificationCodeInput>
```

### Semantic utilities (`src/style.css`, tokens in `src/styles/nuxt-ui-theme.css`)

Do not re-declare these by hand in a page; use the class:

- `surface-card` — legacy card shell (`.card` + the quiet `--app-shadow-card` depth).
- `record-divide` — hairline dividers owned by a record list.
- `icon-orb`, `icon-orb-positive|negative|reversal|pending|neutral` — round icon plate.
- `icon-orb-primary` — stat/page-card lead plate (replaces the old
  `p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25` literal).
- `amount`, `amount-positive`, `amount-negative`, `amount-reversal`, `amount-pending`
  — signed amount colours backed by `--app-amount-*` (light values clear 4.5:1,
  ink values 7.8:1+ against the card).

## Import

You can import components individually:

```typescript
import { DataTable, Pagination, Modal } from '@/components/common'
```

Or import specific components:

```typescript
import DataTable from '@/components/common/DataTable.vue'
```

## Features

All components include:

- **TypeScript support** with proper type definitions
- **Accessibility** with ARIA attributes and keyboard navigation
- **Responsive design** with mobile-friendly layouts
- **TailwindCSS styling** for consistent design
- **Vue 3 Composition API** with `<script setup>`
- **Slot support** for customization
