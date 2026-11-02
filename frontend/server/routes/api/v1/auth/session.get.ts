import { buildSessionProbe } from '../../../../utils/session'
import { readServerSession } from '../../../../utils/sessionCookie'

/**
 * Session probe for the app shell.
 *
 * Returns no tokens: the browser only learns whether the cookie is live, when
 * the access token expires, and which CSRF value to echo on mutations.
 *
 * With no session password the instance simply reports an anonymous,
 * unconfigured session. Public pages still render; nothing claims to be
 * logged in, and a mutation is still refused by the proxy.
 */
export default defineEventHandler((event) => {
  const runtime = useRuntimeConfig(event)
  const sessionPassword = String(runtime.sessionPassword || '')
  setResponseHeader(event, 'cache-control', 'no-store, no-cache, must-revalidate, private')
  setResponseHeader(event, 'vary', 'cookie')

  const configured = Boolean(sessionPassword)
  const session = configured ? readServerSession(event, sessionPassword) : null

  return {
    code: 0,
    message: 'success',
    data: buildSessionProbe(session, configured),
  }
})
