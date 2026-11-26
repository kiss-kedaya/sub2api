package service

import "github.com/gin-gonic/gin"

// ReleaseRequestBodyReferences 在请求处理链结束后，清掉挂在 gin.Context 上、会钉住请求体的
// 几个引用：BodyTop 索引（最近两份请求体）、compat 转发保留的上游请求体、调试转储用的入站体。
//
// 为什么要清：gin 把 Context 放回对象池时不重置它，池里的 Context 还指着最后一个请求的
// Keys；计费任务、日志队列等异步对象也可能带着 Context。不清，已经结束的请求的整份请求体
// （可达数 MB）要等 Context 被复用、或池在两次 GC 后被清空才能回收。
//
// 只能在没有任何后续读取之后调用（请求处理链已经返回）。这些值都是缓存或诊断用途：
// 之后再有人调 RequestBodyTop 只会重建索引，compat/调试读取方在值为空时有各自的回退。
func ReleaseRequestBodyReferences(c *gin.Context) {
	if c == nil {
		return
	}
	if v, ok := c.Get(bodyTopMemoKey); ok {
		if memo, _ := v.(*bodyTopMemo); memo != nil {
			for i := range memo.slots {
				memo.slots[i].Store(nil)
			}
		}
	}
	if _, ok := c.Get(compatUpstreamRequestBodyKey); ok {
		c.Set(compatUpstreamRequestBodyKey, compatRequestStash{})
	}
	if _, ok := c.Get(debugInboundBodyKey); ok {
		c.Set(debugInboundBodyKey, []byte(nil))
	}
}
