// Native Gemini cache capabilities are explicitly declared for custom gateways.
export interface GeminiCacheSettings {
  enabled: boolean
  maxTTLSeconds: number
}

export function readGeminiCacheSettings(credentials?: Record<string, unknown>): GeminiCacheSettings {
  const raw = Number(credentials?.explicit_cache_upstream_max_ttl_seconds ?? 0)
  return {
    enabled: credentials?.explicit_cache_upstream === true,
    maxTTLSeconds: Number.isSafeInteger(raw) && raw > 0 ? Math.min(raw, 86400) : 0
  }
}

export function applyGeminiCacheSettings(
  credentials: Record<string, unknown>,
  settings: GeminiCacheSettings,
  platform: string,
  nativeProtocol: boolean
): void {
  delete credentials.explicit_cache_upstream
  delete credentials.explicit_cache_upstream_max_ttl_seconds
  if (platform !== 'gemini' || !nativeProtocol || !settings.enabled) return
  credentials.explicit_cache_upstream = true
  const ttl = Number(settings.maxTTLSeconds)
  if (Number.isSafeInteger(ttl) && ttl > 0) {
    credentials.explicit_cache_upstream_max_ttl_seconds = Math.min(ttl, 86400)
  }
}
