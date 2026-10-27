import overview from './overview'
import channels from './channels'
import accounts from './accounts'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'
import promptAudit from './promptAudit'
import plugins from './plugins'
import crypto from './crypto'

const messages = {
  ...overview,
  ...channels,
  ...accounts,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
  ...promptAudit,
  ...plugins,
  ...crypto,
}

import groupCacheTest from './groupCacheTest'
import requestCapture from './requestCapture'
import timer from '@/components/account/schedulableTimer.en'
import ipRisk from '@/features/ip-risk/locale.en'
export default {...messages,...groupCacheTest,...requestCapture,accounts:{...messages.accounts,timer},settings:{...messages.settings,ipRisk}}
