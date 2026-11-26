package service

import (
	"reflect"
	"strings"
)

// 请求体里切出来的短字符串（模型名、reasoning effort、prompt_cache_key……）大多是零拷贝子串，
// 指针指向请求体本身。这些字符串一旦被放进「活得比请求久」的对象——计费任务队列、usage_logs
// 批量写入队列、结果对象——一个 20 字节的模型名就会把整份请求体（可达数 MB）钉在内存里，直到
// 队列被消费完。计费库一变慢，队列里排着几千份已经结束的请求体，这是进程内存涨到
// GOMEMLIMIT 的主要放大器。
//
// detachStringFields 把一个结构体上所有导出的 string / *string / []string 字段换成自己的拷贝，
// 值不变，只是不再指向请求体。拷贝只有几十个小字符串，相对一次请求可以忽略。

// DetachRequestStrings 复制结果里所有字符串字段，使其不再引用请求体。
func (r *ForwardResult) DetachRequestStrings() { detachStringFields(r) }

// DetachRequestStrings 复制结果里所有字符串字段，使其不再引用请求体。
func (r *OpenAIForwardResult) DetachRequestStrings() { detachStringFields(r) }

// Detached 返回字符串字段都已复制的副本。
func (f ChannelUsageFields) Detached() ChannelUsageFields {
	detachStringFields(&f)
	return f
}

// detachStringFields 复制 v（必须是指向结构体的指针）的导出 string、*string、[]string 字段。
// *string 换成指向新字符串的新指针，不改被指向的原对象（它可能被别处共享）。
func detachStringFields(v any) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return
	}
	rv = rv.Elem()
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		if !f.CanSet() {
			continue
		}
		switch f.Kind() {
		case reflect.String:
			if f.Len() > 0 {
				f.SetString(strings.Clone(f.String()))
			}
		case reflect.Pointer:
			if f.IsNil() || f.Type().Elem().Kind() != reflect.String {
				continue
			}
			cp := reflect.New(f.Type().Elem())
			cp.Elem().SetString(strings.Clone(f.Elem().String()))
			f.Set(cp)
		case reflect.Slice:
			if f.IsNil() || f.Type().Elem().Kind() != reflect.String {
				continue
			}
			cp := reflect.MakeSlice(f.Type(), f.Len(), f.Len())
			for j := 0; j < f.Len(); j++ {
				cp.Index(j).SetString(strings.Clone(f.Index(j).String()))
			}
			f.Set(cp)
		}
	}
}
