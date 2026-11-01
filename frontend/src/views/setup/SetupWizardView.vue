<template>
  <div class="flex min-h-screen items-start justify-center bg-muted px-4 py-10 sm:items-center">
    <div class="w-full max-w-2xl">
      <!-- Logo & Title -->
      <div class="mb-8 text-center">
        <span class="mb-4 inline-flex size-12 items-center justify-center rounded-xl bg-primary/10 ring ring-inset ring-primary/25">
          <UIcon name="i-lucide-settings" class="size-6 text-primary" />
        </span>
        <h1 class="text-2xl font-bold text-highlighted sm:text-3xl">{{ t('setup.title') }}</h1>
        <p class="mt-2 text-muted">{{ t('setup.description') }}</p>
      </div>

      <!-- Progress Steps -->
      <UStepper
        :model-value="currentStep"
        :items="stepperItems"
        size="sm"
        class="mb-8"
        @update:model-value="goToStep"
      />

      <!-- Step Content -->
      <UCard
        variant="outline"
        :title="steps[currentStep].title"
        :description="stepDescriptions[currentStep]"
        :ui="{ root: 'bg-default', header: 'px-6 py-5 sm:px-8', body: 'px-6 py-6 sm:px-8', footer: 'px-6 py-4 sm:px-8', title: 'text-lg' }"
      >
        <!-- Step 1: Database -->
        <div v-if="currentStep === 0" class="space-y-5">
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <UFormField :label="t('setup.database.host')">
              <UInput v-model="formData.database.host" placeholder="localhost" class="w-full" />
            </UFormField>
            <UFormField :label="t('setup.database.port')">
              <UInput v-model.number="formData.database.port" type="number" placeholder="5432" class="w-full" />
            </UFormField>
          </div>

          <UFormField
            :label="t('setup.redis.enableTls')"
            :description="t('setup.redis.enableTlsHint')"
            orientation="horizontal"
            class="items-center"
          >
            <Toggle v-model="formData.redis.enable_tls" />
          </UFormField>

          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <UFormField :label="t('setup.database.username')">
              <UInput v-model="formData.database.user" placeholder="postgres" class="w-full" />
            </UFormField>
            <UFormField :label="t('setup.database.password')">
              <UInput
                v-model="formData.database.password"
                type="password"
                :placeholder="t('setup.database.passwordPlaceholder')"
                class="w-full"
              />
            </UFormField>
          </div>

          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <UFormField :label="t('setup.database.databaseName')">
              <UInput v-model="formData.database.dbname" placeholder="sub2api" class="w-full" />
            </UFormField>
            <UFormField :label="t('setup.database.sslMode')">
              <Select
                v-model="formData.database.sslmode"
                :options="[
                  { value: 'disable', label: t('setup.database.ssl.disable') },
                  { value: 'require', label: t('setup.database.ssl.require') },
                  { value: 'verify-ca', label: t('setup.database.ssl.verifyCa') },
                  { value: 'verify-full', label: t('setup.database.ssl.verifyFull') }
                ]"
              />
            </UFormField>
          </div>

          <UButton
            :loading="testingDb"
            :disabled="testingDb"
            :icon="dbConnected && !testingDb ? 'i-lucide-check' : undefined"
            :label="testingDb ? t('setup.status.testing') : dbConnected ? t('setup.status.success') : t('setup.status.testConnection')"
            :color="dbConnected ? 'success' : 'neutral'"
            variant="outline"
            block
            @click="testDatabaseConnection"
          />
        </div>

        <!-- Step 2: Redis -->
        <div v-if="currentStep === 1" class="space-y-5">
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <UFormField :label="t('setup.redis.host')">
              <UInput v-model="formData.redis.host" placeholder="localhost" class="w-full" />
            </UFormField>
            <UFormField :label="t('setup.redis.port')">
              <UInput v-model.number="formData.redis.port" type="number" placeholder="6379" class="w-full" />
            </UFormField>
          </div>

          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <UFormField :label="t('setup.redis.username')">
              <UInput v-model="formData.redis.username" :placeholder="t('setup.redis.usernamePlaceholder')" class="w-full" />
            </UFormField>
            <UFormField :label="t('setup.redis.password')">
              <UInput
                v-model="formData.redis.password"
                type="password"
                :placeholder="t('setup.redis.passwordPlaceholder')"
                class="w-full"
              />
            </UFormField>
            <UFormField :label="t('setup.redis.database')">
              <UInput v-model.number="formData.redis.db" type="number" placeholder="0" class="w-full" />
            </UFormField>
          </div>

          <UFormField
            :label="t('setup.redis.enableTls')"
            :description="t('setup.redis.enableTlsHint')"
            orientation="horizontal"
            class="items-center"
          >
            <Toggle v-model="formData.redis.enable_tls" />
          </UFormField>

          <UButton
            :loading="testingRedis"
            :disabled="testingRedis"
            :icon="redisConnected && !testingRedis ? 'i-lucide-check' : undefined"
            :label="testingRedis ? t('setup.status.testing') : redisConnected ? t('setup.status.success') : t('setup.status.testConnection')"
            :color="redisConnected ? 'success' : 'neutral'"
            variant="outline"
            block
            @click="testRedisConnection"
          />
        </div>

        <!-- Step 3: Admin -->
        <div v-if="currentStep === 2" class="space-y-5">
          <UFormField :label="t('setup.admin.email')">
            <UInput v-model="formData.admin.email" type="email" placeholder="admin@example.com" class="w-full" />
          </UFormField>
          <UFormField :label="t('setup.admin.password')">
            <UInput
              v-model="formData.admin.password"
              type="password"
              :placeholder="t('setup.admin.passwordPlaceholder')"
              class="w-full"
            />
          </UFormField>
          <UFormField
            :label="t('setup.admin.confirmPassword')"
            :error="confirmPassword && formData.admin.password !== confirmPassword ? t('setup.admin.passwordMismatch') : false"
          >
            <UInput
              v-model="confirmPassword"
              type="password"
              :placeholder="t('setup.admin.confirmPasswordPlaceholder')"
              class="w-full"
            />
          </UFormField>
        </div>

        <!-- Step 4: Complete -->
        <dl v-if="currentStep === 3" class="divide-y divide-default rounded-lg border border-default">
          <div class="flex flex-col gap-1 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
            <dt class="text-sm text-muted">{{ t('setup.ready.database') }}</dt>
            <dd class="font-mono text-sm text-highlighted wrap-anywhere">
              {{ formData.database.user }}@{{ formData.database.host }}:{{ formData.database.port }}/{{ formData.database.dbname }}
            </dd>
          </div>
          <div class="flex flex-col gap-1 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
            <dt class="text-sm text-muted">{{ t('setup.ready.redis') }}</dt>
            <dd class="font-mono text-sm text-highlighted wrap-anywhere">{{ formData.redis.host }}:{{ formData.redis.port }}</dd>
          </div>
          <div class="flex flex-col gap-1 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
            <dt class="text-sm text-muted">{{ t('setup.ready.adminEmail') }}</dt>
            <dd class="text-sm text-highlighted wrap-anywhere">{{ formData.admin.email }}</dd>
          </div>
        </dl>

        <!-- Error Message -->
        <UAlert
          v-if="errorMessage"
          :description="errorMessage"
          icon="i-lucide-circle-alert"
          color="error"
          variant="subtle"
          class="mt-6"
        />

        <!-- Success Message -->
        <UAlert
          v-if="installSuccess"
          :title="t('setup.status.completed')"
          :description="serviceReady ? t('setup.status.redirecting') : t('setup.status.restarting')"
          :icon="serviceReady ? 'i-lucide-circle-check' : 'i-lucide-loader-circle'"
          color="success"
          variant="subtle"
          class="mt-6"
          :ui="{ icon: serviceReady ? '' : 'animate-spin' }"
        />

        <!-- Navigation Buttons -->
        <template v-if="!installSuccess" #footer>
          <div class="flex justify-between gap-3">
            <UButton
              v-if="currentStep > 0 && !installSuccess"
              :label="t('common.back')"
              icon="i-lucide-chevron-left"
              color="neutral"
              variant="outline"
              @click="currentStep--"
            />
            <span v-else></span>

            <UButton
              v-if="currentStep < 3"
              :label="t('common.next')"
              trailing-icon="i-lucide-chevron-right"
              :disabled="!canProceed"
              @click="nextStep"
            />
            <UButton
              v-else-if="!installSuccess"
              :loading="installing"
              :disabled="installing"
              :label="installing ? t('setup.status.installing') : t('setup.status.completeInstallation')"
              @click="performInstall"
            />
          </div>
        </template>
      </UCard>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { StepperItem } from '@nuxt/ui'
import { testDatabase, testRedis, install, type InstallRequest } from '@/api/setup'
import { buildGatewayUrl } from '@/api/client'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'

const { t } = useI18n()

const steps = computed(() => [
  { id: 'database', title: t('setup.database.title') },
  { id: 'redis', title: t('setup.redis.title') },
  { id: 'admin', title: t('setup.admin.title') },
  { id: 'complete', title: t('setup.ready.title') }
])

const stepDescriptions = computed(() => [
  t('setup.database.description'),
  t('setup.redis.description'),
  t('setup.admin.description'),
  t('setup.ready.description')
])

const currentStep = ref(0)
const errorMessage = ref('')
const installSuccess = ref(false)

// Done steps show a check; later steps stay locked until Next passes their checks.
const stepperItems = computed<StepperItem[]>(() => steps.value.map((step, index) => ({
  title: step.title,
  icon: index < currentStep.value ? 'i-lucide-check' : undefined,
  disabled: installSuccess.value || index > currentStep.value
})))

/** Clicking a finished step goes back to it, like the Back button; never forward. */
function goToStep(value: string | number | undefined) {
  const index = Number(value)
  if (installSuccess.value || !Number.isInteger(index) || index >= currentStep.value) return
  errorMessage.value = ''
  currentStep.value = index
}

// Connection test states
const testingDb = ref(false)
const testingRedis = ref(false)
const dbConnected = ref(false)
const redisConnected = ref(false)
const installing = ref(false)
const confirmPassword = ref('')
const serviceReady = ref(false)

// Default server port
const getCurrentPort = (): number => {
  const port = window.location.port
  if (port) {
    return parseInt(port, 10)
  }

  return window.location.protocol === 'https:' ? 443 : 80
}

const formData = reactive<InstallRequest>({
  database: {
    host: 'localhost',
    port: 5432,
    user: 'postgres',
    password: '',
    dbname: 'sub2api',
    sslmode: 'disable'
  },
  redis: {
    host: 'localhost',
    port: 6379,
    username: '',
    password: '',
    db: 0,
    enable_tls: false
  },
  admin: {
    email: '',
    password: ''
  },
  server: {
    host: '0.0.0.0',
    port: getCurrentPort(), // Use current port from browser
    mode: 'release'
  }
})

const canProceed = computed(() => {
  switch (currentStep.value) {
    case 0:
      return dbConnected.value
    case 1:
      return redisConnected.value
    case 2:
      return (
        formData.admin.email &&
        formData.admin.password.length >= 8 &&
        formData.admin.password === confirmPassword.value
      )
    default:
      return true
  }
})

async function testDatabaseConnection() {
  testingDb.value = true
  errorMessage.value = ''
  dbConnected.value = false

  try {
    await testDatabase(formData.database)
    dbConnected.value = true
  } catch (error: unknown) {
    const err = error as { response?: { data?: { detail?: string; message?: string } }; message?: string }
    errorMessage.value =
      err.response?.data?.detail || err.response?.data?.message || err.message || 'Connection failed'
  } finally {
    testingDb.value = false
  }
}

async function testRedisConnection() {
  testingRedis.value = true
  errorMessage.value = ''
  redisConnected.value = false

  try {
    await testRedis(formData.redis)
    redisConnected.value = true
  } catch (error: unknown) {
    const err = error as { response?: { data?: { detail?: string; message?: string } }; message?: string }
    errorMessage.value =
      err.response?.data?.detail || err.response?.data?.message || err.message || 'Connection failed'
  } finally {
    testingRedis.value = false
  }
}

function nextStep() {
  if (canProceed.value) {
    errorMessage.value = ''
    currentStep.value++
  }
}

async function performInstall() {
  installing.value = true
  errorMessage.value = ''

  try {
    await install(formData)
    installSuccess.value = true
    // Start polling for service restart
    waitForServiceRestart()
  } catch (error: unknown) {
    const err = error as { response?: { data?: { detail?: string; message?: string } }; message?: string }
    errorMessage.value =
      err.response?.data?.detail || err.response?.data?.message || err.message || 'Installation failed'
  } finally {
    installing.value = false
  }
}

// Wait for service to restart and become available
async function waitForServiceRestart() {
  const maxAttempts = 60 // Increase to 60 attempts, ~60 seconds max
  const interval = 1000 // 1 second between attempts

  // Wait a moment for the service to start restarting
  await new Promise((resolve) => setTimeout(resolve, 3000))

  for (let attempt = 0; attempt < maxAttempts; attempt++) {
    try {
      // Use setup status endpoint as it tells us the real mode
      // Service might return 404 or connection refused while restarting
      const response = await fetch(buildGatewayUrl('/setup/status'), {
        method: 'GET',
        cache: 'no-store'
      })

      if (response.ok) {
        const data = await response.json()
        // If needs_setup is false, service has restarted in normal mode
        if (data.data && !data.data.needs_setup) {
          serviceReady.value = true
          // Redirect to login page after a short delay
          setTimeout(() => {
            window.location.href = '/login'
          }, 1500)
          return
        }
      }
    } catch {
      // Service not ready or network error during restart, continue polling
    }

    await new Promise((resolve) => setTimeout(resolve, interval))
  }

  // If we reach here, service didn't restart in time
  // Show a message to refresh manually
  errorMessage.value = t('setup.status.timeout')
}
</script>
