package service

import "fmt"

// 一遍扫描收集“补丁”、最后一次精确大小输出的改写原语。
//
// 背景：sjson.SetBytes/DeleteBytes 每调用一次就整体复制一次请求体（还带 append 扩容的
// 额外复制和最多 25% 的尾部空闲容量）。对象成员的批量删除如果写成循环，就是
// O(成员数 × 请求体大小) 的分配，而且每一份中间复制品都是 GC 压力。
// 这里把“要删/要换的字节区间”先记下来，最后只分配一次（len = 精确结果长度）。

// jsonObjMember 是对象里一个成员的字节区间 [start, end)：从键的起始引号到值的末尾。
type jsonObjMember struct{ start, end int }

// jsonEdit 把 body[start:end] 换成 replacement；replacement 为空就是删除这段。
// 一组 edit 必须按 start 升序且互不重叠。
type jsonEdit struct {
	start, end  int
	replacement string
}

// jsonObjMembersTight 判断以 body[objStart]=='{' 开头的对象，其成员列表（按出现顺序给全）
// 是否是紧凑排版 {"k":v,"k":v}：第一个键紧跟在 '{' 后，成员之间恰好一个 ',' 且两侧没有
// 空白，最后一个值后面紧跟 '}'。
//
// 只有这种排版下，“删掉成员和相邻的一个逗号”才和 sjson.DeleteBytes 逐字节一致
// （sjson 会保留它删掉的那个逗号两侧的空白，宽松排版下结果不同）。其它排版一律交回
// sjson 处理，保证行为不变。
func jsonObjMembersTight(body []byte, objStart int, members []jsonObjMember) bool {
	n := len(members)
	if n == 0 || members[0].start != objStart+1 {
		return false
	}
	for i := 0; i+1 < n; i++ {
		end := members[i].end
		if end >= len(body) || body[end] != ',' || members[i+1].start != end+1 {
			return false
		}
	}
	last := members[n-1].end
	return last < len(body) && body[last] == '}'
}

// appendJSONMemberDrops 追加“从紧凑对象里删除 members[drop...]”所需的 edit。
// drop 是升序、不重复的成员下标。结果等于 { 剩余成员用 ',' 连接 }：
//   - 一段连续被删的成员后面还有成员：删到下一个成员的键起点（连带这段里的逗号）；
//   - 被删的段延伸到最后一个成员：从前一个成员的末尾（即前面那个逗号）删到最后；
//   - 全部被删：删掉整个成员区，留下 {}。
func appendJSONMemberDrops(edits []jsonEdit, members []jsonObjMember, drop ...int) []jsonEdit {
	n := len(members)
	for k := 0; k < len(drop); {
		i, j := drop[k], drop[k]
		for k++; k < len(drop) && drop[k] == j+1; k++ {
			j = drop[k]
		}
		switch {
		case j < n-1:
			edits = append(edits, jsonEdit{start: members[i].start, end: members[j+1].start})
		case i > 0:
			edits = append(edits, jsonEdit{start: members[i-1].end, end: members[j].end})
		default:
			edits = append(edits, jsonEdit{start: members[0].start, end: members[n-1].end})
		}
	}
	return edits
}

// applyJSONEdits 一次分配、一次复制地应用 edits：未改动的字节只复制一遍，
// 返回值长度就是精确结果长度（没有 append 扩容留下的空闲容量）。
func applyJSONEdits(body []byte, edits []jsonEdit) ([]byte, error) {
	size, prev := len(body), 0
	for _, e := range edits {
		if e.start < prev || e.end < e.start || e.end > len(body) {
			return nil, fmt.Errorf("invalid JSON edit span [%d,%d) in %d bytes", e.start, e.end, len(body))
		}
		prev = e.end
		size += len(e.replacement) - (e.end - e.start)
	}
	out := make([]byte, 0, size)
	prev = 0
	for _, e := range edits {
		out = append(out, body[prev:e.start]...)
		out = append(out, e.replacement...)
		prev = e.end
	}
	return append(out, body[prev:]...), nil
}
