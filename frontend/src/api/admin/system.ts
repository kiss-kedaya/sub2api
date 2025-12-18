/**
 * System API endpoints for admin operations
 */

import { apiClient } from '../client';

export interface ReleaseInfo {
  name: string;
  body: string;
  published_at: string;
  html_url: string;
}

export interface VersionInfo {
  current_version: string;
  latest_version: string;
  has_update: boolean;
  release_info?: ReleaseInfo;
  cached: boolean;
  warning?: string;
  build_type: string; // "source" for manual builds, "release" for CI builds
}

/**
 * Get current version
 */
export async function getVersion(): Promise<{ version: string }> {
  const { data } = await apiClient.get<{ version: string }>('/admin/system/version');
  return data;
}

/**
 * Check for updates
 * @param force - Force refresh from GitHub API
 */
export async function checkUpdates(force = false): Promise<VersionInfo> {
  const { data } = await apiClient.get<VersionInfo>('/admin/system/check-updates', {
    params: force ? { force: 'true' } : undefined,
  });
  return data;
}

export const systemAPI = {
  getVersion,
  checkUpdates,
};

export default systemAPI;
