import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import channelMonitorV3 from './channelMonitorV3'
import batchImage from './batchImage'
import tickets from './tickets'
import admin from './admin'
import misc from './misc'
import deposit from './deposit'
import docs from './docs'
import connections from './connections'
import requestTiming from './requestTiming'
import traffic from './traffic'
import ipAudit from '@/features/ip-audit/locale.zh'
import {zh as moderationCategories} from '@/features/ip-risk/moderationCategories.locale'

import accountOps from '@/components/admin/operations/accountOps.zh'
import channelOps from '@/components/admin/operations/channelOps.zh'

export default {
  accountOps, channelOps,
  qualityOps: { refresh: '刷新', accounts: '账号', loadMore: '加载更多' },
  ...landing,
  ...common,
  ...dashboard,
  ...channelMonitorV2,
  ...channelMonitorV3,
  ...batchImage,
  ...tickets,
  admin,
  ...misc,
  ...deposit,
  ...docs,
  connections,requestTiming,traffic,ipAudit,moderationCategories,
}
