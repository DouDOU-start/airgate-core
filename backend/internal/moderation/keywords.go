package moderation

import "strings"

// keywordMatcher 关键词命中的 Aho-Corasick 自动机（大小写不敏感、按字节走转移表）。
// 命中多个关键词时返回配置序最靠前的一个（bestKeyword 取最小下标）。
type keywordMatcher struct {
	nodes           []keywordNode
	edges           []keywordEdge
	rootTransitions [256]int32
	keywords        []string
}

type keywordNode struct {
	failure     int32
	bestKeyword int32
	edgeStart   uint32
	edgeCount   uint16
}

type keywordEdge struct {
	target int32
	label  byte
}

type keywordBuildEdge struct {
	target      int32
	nextSibling int32
	label       byte
}

func newKeywordMatcher(keywords []string) *keywordMatcher {
	if len(keywords) == 0 {
		return nil
	}

	buildNodes := []keywordNode{newKeywordNode()}
	buildEdges := make([]keywordBuildEdge, 0)
	originalKeywords := append([]string(nil), keywords...)

	for keywordIndex, keyword := range keywords {
		if keyword == "" {
			continue
		}
		state := int32(0)
		for _, label := range []byte(strings.ToLower(keyword)) {
			next := keywordBuildTransition(buildNodes, buildEdges, state, label)
			if next < 0 {
				next = int32(len(buildNodes))
				buildNodes = append(buildNodes, newKeywordNode())
				buildEdges = append(buildEdges, keywordBuildEdge{
					target:      next,
					nextSibling: keywordBuildFirstEdge(buildNodes[state]),
					label:       label,
				})
				buildNodes[state].edgeStart = uint32(len(buildEdges))
			}
			state = next
		}
		if current := buildNodes[state].bestKeyword; current < 0 || int32(keywordIndex) < current {
			buildNodes[state].bestKeyword = int32(keywordIndex)
		}
	}

	if len(buildNodes) == 1 {
		return nil
	}

	queue := make([]int32, 0, len(buildNodes)-1)
	var rootTransitions [256]int32
	for edgeIndex := keywordBuildFirstEdge(buildNodes[0]); edgeIndex >= 0; edgeIndex = buildEdges[edgeIndex].nextSibling {
		edge := buildEdges[edgeIndex]
		rootTransitions[edge.label] = edge.target
		queue = append(queue, edge.target)
	}

	for queueIndex := 0; queueIndex < len(queue); queueIndex++ {
		state := queue[queueIndex]
		for edgeIndex := keywordBuildFirstEdge(buildNodes[state]); edgeIndex >= 0; edgeIndex = buildEdges[edgeIndex].nextSibling {
			edge := buildEdges[edgeIndex]
			failure := buildNodes[state].failure
			fallback := keywordBuildTransition(buildNodes, buildEdges, failure, edge.label)
			for fallback < 0 && failure != 0 {
				failure = buildNodes[failure].failure
				fallback = keywordBuildTransition(buildNodes, buildEdges, failure, edge.label)
			}
			if fallback >= 0 {
				buildNodes[edge.target].failure = fallback
			}
			buildNodes[edge.target].bestKeyword = minKeywordIndex(
				buildNodes[edge.target].bestKeyword,
				buildNodes[buildNodes[edge.target].failure].bestKeyword,
			)
			queue = append(queue, edge.target)
		}
	}

	edges := make([]keywordEdge, 0, len(buildEdges))
	var outgoing [256]keywordEdge
	for nodeIndex := range buildNodes {
		count := 0
		for edgeIndex := keywordBuildFirstEdge(buildNodes[nodeIndex]); edgeIndex >= 0; edgeIndex = buildEdges[edgeIndex].nextSibling {
			edge := buildEdges[edgeIndex]
			outgoing[count] = keywordEdge{target: edge.target, label: edge.label}
			count++
		}
		for index := 1; index < count; index++ {
			current := outgoing[index]
			insertAt := index
			for insertAt > 0 && current.label < outgoing[insertAt-1].label {
				outgoing[insertAt] = outgoing[insertAt-1]
				insertAt--
			}
			outgoing[insertAt] = current
		}
		buildNodes[nodeIndex].edgeStart = uint32(len(edges))
		buildNodes[nodeIndex].edgeCount = uint16(count)
		edges = append(edges, outgoing[:count]...)
	}

	return &keywordMatcher{
		nodes:           buildNodes,
		edges:           edges,
		rootTransitions: rootTransitions,
		keywords:        originalKeywords,
	}
}

func newKeywordNode() keywordNode {
	return keywordNode{bestKeyword: -1}
}

func keywordBuildFirstEdge(node keywordNode) int32 {
	if node.edgeStart == 0 {
		return -1
	}
	return int32(node.edgeStart - 1)
}

func keywordBuildTransition(nodes []keywordNode, edges []keywordBuildEdge, state int32, label byte) int32 {
	if state < 0 || int(state) >= len(nodes) {
		return -1
	}
	for edgeIndex := keywordBuildFirstEdge(nodes[state]); edgeIndex >= 0; edgeIndex = edges[edgeIndex].nextSibling {
		if edges[edgeIndex].label == label {
			return edges[edgeIndex].target
		}
	}
	return -1
}

func minKeywordIndex(left, right int32) int32 {
	if left < 0 {
		return right
	}
	if right < 0 || left < right {
		return left
	}
	return right
}

// Match 返回命中的关键词（配置序最靠前者）。
func (m *keywordMatcher) Match(text string) (string, bool) {
	if m == nil || text == "" || len(m.nodes) == 0 || len(m.keywords) == 0 {
		return "", false
	}
	lower := strings.ToLower(text)
	state := int32(0)
	bestKeyword := int32(-1)
	for index := 0; index < len(lower); index++ {
		label := lower[index]
		for {
			next := m.next(state, label)
			if next != 0 {
				state = next
				break
			}
			if state == 0 {
				break
			}
			state = m.nodes[state].failure
		}
		bestKeyword = minKeywordIndex(bestKeyword, m.nodes[state].bestKeyword)
		if bestKeyword == 0 {
			return m.keywords[0], true
		}
	}
	if bestKeyword < 0 || int(bestKeyword) >= len(m.keywords) {
		return "", false
	}
	return m.keywords[bestKeyword], true
}

func (m *keywordMatcher) next(state int32, label byte) int32 {
	if state == 0 {
		return m.rootTransitions[label]
	}
	if state < 0 || int(state) >= len(m.nodes) {
		return 0
	}
	node := m.nodes[state]
	left := int(node.edgeStart)
	right := left + int(node.edgeCount)
	for left < right {
		middle := left + (right-left)/2
		edge := m.edges[middle]
		if edge.label < label {
			left = middle + 1
			continue
		}
		right = middle
	}
	end := int(node.edgeStart) + int(node.edgeCount)
	if left < end && m.edges[left].label == label {
		return m.edges[left].target
	}
	return 0
}
