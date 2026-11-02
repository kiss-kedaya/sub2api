interface ClientRuntimeState {
  app: { publicSettingsLoaded: boolean; fetchPublicSettings: () => Promise<unknown> }
  auth: { cookieSession: boolean; user: unknown; refreshUser: () => Promise<unknown> }
  probe: () => Promise<{ data?: { authenticated?: boolean; csrf_token?: string } }>
}

/** Restore a cookie login before client-only routes run their navigation guards. */
export async function restoreClientRuntimeSession({ app, auth, probe }: ClientRuntimeState): Promise<string | null> {
  const settings = app.publicSettingsLoaded ? Promise.resolve() : app.fetchPublicSettings().catch(() => undefined)
  let csrf: string | null = null
  try {
    const result = await probe()
    csrf = result.data?.csrf_token ?? null
    if (result.data?.authenticated && !auth.user) {
      auth.cookieSession = true
      await auth.refreshUser()
    }
  } catch {
    // Never invent a user or clear an already hydrated session on a transient read failure.
  }
  await settings
  return csrf
}
