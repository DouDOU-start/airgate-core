package cursor

import (
	"crypto/sha256"
	"encoding/hex"
)

// BlobStore 是单次 Run 期间的 KV blob 暂存：Cursor 的 conversationState 里
// rootPromptMessages / turns 只携带 blobId（内容的 sha256），服务端在流中通过
// KvService.getBlob 反向索取真实内容，本地从这里应答。键为 blobId 的 hex。
type BlobStore map[string][]byte

// NewBlobStore 创建空 blob 暂存。
func NewBlobStore() BlobStore {
	return make(BlobStore)
}

// Store 存入一段数据并返回其 blobId（sha256）。相同内容天然去重（同 id）。
func (b BlobStore) Store(data []byte) []byte {
	sum := sha256.Sum256(data)
	id := sum[:]
	b[hex.EncodeToString(id)] = data
	return id
}

// Get 按 blobId 取回内容。
func (b BlobStore) Get(id []byte) ([]byte, bool) {
	data, ok := b[hex.EncodeToString(id)]
	return data, ok
}

// Set 直接以给定 blobId 存入内容，用于应答服务端的 setBlob。
func (b BlobStore) Set(id, data []byte) {
	b[hex.EncodeToString(id)] = data
}
