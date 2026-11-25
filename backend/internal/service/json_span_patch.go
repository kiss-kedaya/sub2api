package service

import (
	"slices"

	"github.com/tidwall/gjson"
)

// jsonSpanPatcher 对同一份不可变的 JSON 文本收集若干处小改动（替换某个值、在对象末尾追加成员、
// 在某处插入文本），最后一次分配、一次拷贝地输出。
//
// 它取代「循环里每处改动调一次 sjson.Set*」的写法：sjson 每次都整体复制请求体并从头重新定位路径，
// N 处改动就是 O(N×请求体) 的分配与扫描（生产 profile 里单这一个形状占全部分配的一半）；
// 这里先扫一遍收集补丁、再分配一次，是 O(请求体)。
//
// 约定：
//   - 所有区间都是相对 doc 原文的偏移；doc 在 patcher 存活期间不得被改写。
//   - gjson.Result 必须来自对 doc（或其零拷贝 string 视图）的查询；Index/Raw 与 doc 对不上时
//     （含 Index 未知为 0 的情况）会让整个 patcher 失效，apply 返回 changed=false，
//     调用方原样返回自己的入参——和原来 sjson 报错时的行为一致。
//   - 输出里被改动的位置与 sjson 的写法一致：替换只换值本身的区间；追加成员写在对象最后一个 '}' 之前，
//     非空对象先补逗号（空对象不补）。未改动的字节（含缩进、键序）原样保留。
type jsonSpanPatcher struct {
	doc      []byte
	patches  []jsonSpanPatch
	delta    int  // 输出比 doc 多出（负数为少掉）的字节数
	unsorted bool // 补丁没有按偏移顺序加入，apply 时需要排序
	bad      bool
}

type jsonSpanPatch struct {
	start, end int // 替换 doc[start:end]；start==end 是纯插入
	comma      bool
	text       string
}

func newJSONSpanPatcher(doc []byte) *jsonSpanPatcher {
	return &jsonSpanPatcher{doc: doc}
}

// jsonResultSpan 返回 v 在 doc 里的 [start,end)。v.Index/v.Raw 和 doc 对不上时 ok=false。
// v 若来自 doc 的零拷贝视图，下面的比较是同一块内存，memequal 直接按指针相等返回，O(1)。
func jsonResultSpan(doc []byte, v gjson.Result) (start, end int, ok bool) {
	start, end = v.Index, v.Index+len(v.Raw)
	if start <= 0 || end <= start || end > len(doc) || string(doc[start:end]) != v.Raw {
		return 0, 0, false
	}
	return start, end, true
}

func (p *jsonSpanPatcher) add(start, end int, comma bool, text string) {
	if n := len(p.patches); n > 0 && start < p.patches[n-1].end {
		p.unsorted = true
	}
	p.patches = append(p.patches, jsonSpanPatch{start: start, end: end, comma: comma, text: text})
	p.delta += len(text) - (end - start)
	if comma {
		p.delta++
	}
}

// replace 把 v 的原文整体换成 text（text 是完整的 JSON 值）。
func (p *jsonSpanPatcher) replace(v gjson.Result, text string) {
	start, end, ok := jsonResultSpan(p.doc, v)
	if !ok {
		p.bad = true
		return
	}
	p.add(start, end, false, text)
}

// insertAt 在原文偏移 at 处插入 text，不删除任何字节。
func (p *jsonSpanPatcher) insertAt(at int, text string) {
	if at < 0 || at > len(p.doc) {
		p.bad = true
		return
	}
	p.add(at, at, false, text)
}

// insertAfterOpen 在数组/对象 v 的开括号之后插入 text（调用方负责 text 末尾的逗号，且 v 非空）。
func (p *jsonSpanPatcher) insertAfterOpen(v gjson.Result, text string) {
	start, end, ok := jsonResultSpan(p.doc, v)
	if !ok || (p.doc[start] != '[' && p.doc[start] != '{') || end-start < 2 {
		p.bad = true
		return
	}
	p.add(start+1, start+1, false, text)
}

// appendMember 在对象 obj 的最后一个 '}' 之前追加成员（member 形如 `"key":value`）。
func (p *jsonSpanPatcher) appendMember(obj gjson.Result, member string) {
	start, end, ok := jsonResultSpan(p.doc, obj)
	if !ok {
		p.bad = true
		return
	}
	p.appendMemberIn(start, end, member)
}

// appendMemberIn 同 appendMember，对象区间 [start,end) 由调用方给出（根对象的 Index 可能是 0，
// 没法走 jsonResultSpan）。
func (p *jsonSpanPatcher) appendMemberIn(start, end int, member string) {
	if start < 0 || end-start < 2 || end > len(p.doc) || p.doc[start] != '{' || p.doc[end-1] != '}' {
		p.bad = true
		return
	}
	// 与 sjson 判断「对象非空」的规则一致：'{' 之后第一个非空白字符不是 '}' 或 ']'。
	comma := false
	for _, c := range p.doc[start+1 : end] {
		if c <= ' ' {
			continue
		}
		comma = c != '}' && c != ']'
		break
	}
	p.add(end-1, end-1, comma, member)
}

// dirty 报告是否已经收集到补丁。
func (p *jsonSpanPatcher) dirty() bool { return len(p.patches) > 0 }

// applyIfValid 同 apply，但只在被扫描的区域 region（messages/input 数组）是合法 JSON 时才输出。
// 对非法 JSON，gjson 的遍历与 sjson 逐次重新定位对「垃圾」的理解并不一致，给它打补丁只会得到更坏的 JSON，
// 所以原样返回（对应原来 sjson 出错时的行为）；各入口在进入这些函数之前本来就已经校验过 JSON，这里是兜底。
// 校验比整个函数的其余部分还便宜（约 1.8 GB/s，不分配）。
func (p *jsonSpanPatcher) applyIfValid(region gjson.Result) ([]byte, bool) {
	if !p.dirty() || !gjson.Valid(region.Raw) {
		return p.doc, false
	}
	return p.apply()
}

// apply 输出打完补丁的文本。没有补丁、或补丁无效（越界、重叠、与原文对不上）时返回 doc 本身
// （同一个切片）和 changed=false；否则返回恰好 len(doc)+delta 字节的新切片。
func (p *jsonSpanPatcher) apply() (out []byte, changed bool) {
	if p.bad || len(p.patches) == 0 {
		return p.doc, false
	}
	if p.unsorted {
		slices.SortStableFunc(p.patches, func(a, b jsonSpanPatch) int {
			if a.start != b.start {
				return a.start - b.start
			}
			return a.end - b.end
		})
	}
	pos := 0
	for _, pt := range p.patches {
		if pt.start < pos || pt.end < pt.start || pt.end > len(p.doc) {
			return p.doc, false
		}
		pos = pt.end
	}
	out = make([]byte, 0, len(p.doc)+p.delta)
	pos = 0
	for _, pt := range p.patches {
		out = append(out, p.doc[pos:pt.start]...)
		if pt.comma {
			out = append(out, ',')
		}
		out = append(out, pt.text...)
		pos = pt.end
	}
	out = append(out, p.doc[pos:]...)
	return out, true
}
