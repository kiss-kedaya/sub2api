/**
 * Application State Store
 * Manages global UI state including sidebar, loading indicators, and toast notifications
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import type { Toast, ToastType } from '@/types';
import { checkUpdates as checkUpdatesAPI, type VersionInfo, type ReleaseInfo } from '@/api/admin/system';

export const useAppStore = defineStore('app', () => {
  // ==================== State ====================

  const sidebarCollapsed = ref<boolean>(false);
  const loading = ref<boolean>(false);
  const toasts = ref<Toast[]>([]);

  // Version cache state
  const versionLoaded = ref<boolean>(false);
  const versionLoading = ref<boolean>(false);
  const currentVersion = ref<string>('');
  const latestVersion = ref<string>('');
  const hasUpdate = ref<boolean>(false);
  const buildType = ref<string>('source');
  const releaseInfo = ref<ReleaseInfo | null>(null);

  // Auto-incrementing ID for toasts
  let toastIdCounter = 0;

  // ==================== Computed ====================
  
  const hasActiveToasts = computed(() => toasts.value.length > 0);
  
  const loadingCount = ref<number>(0);

  // ==================== Actions ====================

  /**
   * Toggle sidebar collapsed state
   */
  function toggleSidebar(): void {
    sidebarCollapsed.value = !sidebarCollapsed.value;
  }

  /**
   * Set sidebar collapsed state explicitly
   * @param collapsed - Whether sidebar should be collapsed
   */
  function setSidebarCollapsed(collapsed: boolean): void {
    sidebarCollapsed.value = collapsed;
  }

  /**
   * Set global loading state
   * @param isLoading - Whether app is in loading state
   */
  function setLoading(isLoading: boolean): void {
    if (isLoading) {
      loadingCount.value++;
    } else {
      loadingCount.value = Math.max(0, loadingCount.value - 1);
    }
    loading.value = loadingCount.value > 0;
  }

  /**
   * Show a toast notification
   * @param type - Type of toast (success, error, info, warning)
   * @param message - Toast message content
   * @param duration - Auto-dismiss duration in ms (undefined = no auto-dismiss)
   * @returns Toast ID for manual dismissal
   */
  function showToast(
    type: ToastType,
    message: string,
    duration?: number
  ): string {
    const id = `toast-${++toastIdCounter}`;
    const toast: Toast = {
      id,
      type,
      message,
      duration,
      startTime: duration !== undefined ? Date.now() : undefined,
    };

    toasts.value.push(toast);

    // Auto-dismiss if duration is specified
    if (duration !== undefined) {
      setTimeout(() => {
        hideToast(id);
      }, duration);
    }

    return id;
  }

  /**
   * Show a success toast
   * @param message - Success message
   * @param duration - Auto-dismiss duration in ms (default: 3000)
   */
  function showSuccess(message: string, duration: number = 3000): string {
    return showToast('success', message, duration);
  }

  /**
   * Show an error toast
   * @param message - Error message
   * @param duration - Auto-dismiss duration in ms (default: 5000)
   */
  function showError(message: string, duration: number = 5000): string {
    return showToast('error', message, duration);
  }

  /**
   * Show an info toast
   * @param message - Info message
   * @param duration - Auto-dismiss duration in ms (default: 3000)
   */
  function showInfo(message: string, duration: number = 3000): string {
    return showToast('info', message, duration);
  }

  /**
   * Show a warning toast
   * @param message - Warning message
   * @param duration - Auto-dismiss duration in ms (default: 4000)
   */
  function showWarning(message: string, duration: number = 4000): string {
    return showToast('warning', message, duration);
  }

  /**
   * Hide a specific toast by ID
   * @param id - Toast ID to hide
   */
  function hideToast(id: string): void {
    const index = toasts.value.findIndex((t) => t.id === id);
    if (index !== -1) {
      toasts.value.splice(index, 1);
    }
  }

  /**
   * Clear all toasts
   */
  function clearAllToasts(): void {
    toasts.value = [];
  }

  /**
   * Execute an async operation with loading state
   * Automatically manages loading indicator
   * @param operation - Async operation to execute
   * @returns Promise resolving to operation result
   */
  async function withLoading<T>(operation: () => Promise<T>): Promise<T> {
    setLoading(true);
    try {
      return await operation();
    } finally {
      setLoading(false);
    }
  }

  /**
   * Execute an async operation with loading and error handling
   * Shows error toast on failure
   * @param operation - Async operation to execute
   * @param errorMessage - Custom error message (optional)
   * @returns Promise resolving to operation result or null on error
   */
  async function withLoadingAndError<T>(
    operation: () => Promise<T>,
    errorMessage?: string
  ): Promise<T | null> {
    setLoading(true);
    try {
      return await operation();
    } catch (error) {
      const message =
        errorMessage ||
        (error as { message?: string }).message ||
        'An error occurred';
      showError(message);
      return null;
    } finally {
      setLoading(false);
    }
  }

  /**
   * Reset app state to defaults
   * Useful for cleanup or testing
   */
  function reset(): void {
    sidebarCollapsed.value = false;
    loading.value = false;
    loadingCount.value = 0;
    toasts.value = [];
  }

  // ==================== Version Management ====================

  /**
   * Fetch version info (uses cache unless force=true)
   * @param force - Force refresh from API
   */
  async function fetchVersion(force = false): Promise<VersionInfo | null> {
    // Return cached data if available and not forcing refresh
    if (versionLoaded.value && !force) {
      return {
        current_version: currentVersion.value,
        latest_version: latestVersion.value,
        has_update: hasUpdate.value,
        build_type: buildType.value,
        release_info: releaseInfo.value || undefined,
        cached: true,
      };
    }

    // Prevent duplicate requests
    if (versionLoading.value) {
      return null;
    }

    versionLoading.value = true;
    try {
      const data = await checkUpdatesAPI(force);
      currentVersion.value = data.current_version;
      latestVersion.value = data.latest_version;
      hasUpdate.value = data.has_update;
      buildType.value = data.build_type || 'source';
      releaseInfo.value = data.release_info || null;
      versionLoaded.value = true;
      return data;
    } catch (error) {
      console.error('Failed to fetch version:', error);
      return null;
    } finally {
      versionLoading.value = false;
    }
  }

  /**
   * Clear version cache (e.g., after update)
   */
  function clearVersionCache(): void {
    versionLoaded.value = false;
    hasUpdate.value = false;
  }

  // ==================== Return Store API ====================

  return {
    // State
    sidebarCollapsed,
    loading,
    toasts,

    // Version state
    versionLoaded,
    versionLoading,
    currentVersion,
    latestVersion,
    hasUpdate,
    buildType,
    releaseInfo,

    // Computed
    hasActiveToasts,

    // Actions
    toggleSidebar,
    setSidebarCollapsed,
    setLoading,
    showToast,
    showSuccess,
    showError,
    showInfo,
    showWarning,
    hideToast,
    clearAllToasts,
    withLoading,
    withLoadingAndError,
    reset,

    // Version actions
    fetchVersion,
    clearVersionCache,
  };
});
