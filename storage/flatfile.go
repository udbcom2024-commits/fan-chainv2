package storage

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"golang.org/x/crypto/sha3"
)

// Flat File 存储设计
// 按fan.md规范：10000块/文件，按高度式命名
// 文件命名：chunk_50000.dat 存储区块 50000-59999
//
// 数据格式（idx索引 + dat数据 + 内嵌哈希）：
// idx文件：每条目16字节 = [8字节偏移量][4字节长度][4字节高度]
// dat文件：每区块 = [N字节区块数据][32字节SHA3哈希]
//
// 设计决策：
// - idx索引文件：O(1)快速定位，主网高性能读取
// - 内嵌SHA3哈希：History节点可校验数据完整性
// - 主网默认不校验：信任本地数据，最大化速度
// - History按需校验：公众入口必须可验证

const (
	ChunkSize       = 10000 // 每个文件存储10000个区块
	IndexEntrySize  = 16    // 索引条目大小（8+4+4字节）
	HashSize        = 32    // SHA3-256哈希大小
	BlocksSubdir    = "blocks"
)

// IndexEntry 索引条目结构
type IndexEntry struct {
	Offset uint64 // 在dat文件中的偏移量
	Length uint32 // 区块数据长度
	Height uint32 // 区块高度（校验用）
}

// BlockStore Flat File区块存储
type BlockStore struct {
	dataDir   string              // 数据根目录
	blocksDir string              // blocks子目录
	mu        sync.RWMutex        // 读写锁
	datFiles  map[uint64]*os.File // 缓存的dat文件句柄
	idxFiles  map[uint64]*os.File // 缓存的idx文件句柄
}

// NewBlockStore 创建区块存储
func NewBlockStore(dataDir string) (*BlockStore, error) {
	blocksDir := filepath.Join(dataDir, BlocksSubdir)

	// 创建blocks目录
	if err := os.MkdirAll(blocksDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create blocks directory: %v", err)
	}

	return &BlockStore{
		dataDir:   dataDir,
		blocksDir: blocksDir,
		datFiles:  make(map[uint64]*os.File),
		idxFiles:  make(map[uint64]*os.File),
	}, nil
}

// Close 关闭所有打开的文件句柄
func (bs *BlockStore) Close() error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	for _, f := range bs.datFiles {
		f.Close()
	}
	for _, f := range bs.idxFiles {
		f.Close()
	}

	bs.datFiles = make(map[uint64]*os.File)
	bs.idxFiles = make(map[uint64]*os.File)

	return nil
}

// getChunkStart 计算分片起始高度
func getChunkStart(height uint64) uint64 {
	return (height / ChunkSize) * ChunkSize
}

// getChunkFiles 获取分片文件路径
func (bs *BlockStore) getChunkFiles(chunkStart uint64) (datPath, idxPath string) {
	datPath = filepath.Join(bs.blocksDir, fmt.Sprintf("chunk_%d.dat", chunkStart))
	idxPath = filepath.Join(bs.blocksDir, fmt.Sprintf("chunk_%d.idx", chunkStart))
	return
}

// getDatFile 获取dat文件句柄（带缓存和有效性检查）
func (bs *BlockStore) getDatFile(chunkStart uint64, create bool) (*os.File, error) {
	if f, ok := bs.datFiles[chunkStart]; ok {
		// 检查文件句柄是否仍然有效
		if _, err := f.Stat(); err == nil {
			return f, nil
		}
		// 句柄无效，关闭并从缓存删除
		f.Close()
		delete(bs.datFiles, chunkStart)
	}

	datPath, _ := bs.getChunkFiles(chunkStart)

	var f *os.File
	var err error
	if create {
		f, err = os.OpenFile(datPath, os.O_RDWR|os.O_CREATE, 0644)
	} else {
		f, err = os.OpenFile(datPath, os.O_RDONLY, 0644)
	}

	if err != nil {
		return nil, err
	}

	bs.datFiles[chunkStart] = f
	return f, nil
}

// getIdxFile 获取idx文件句柄（带缓存和有效性检查）
func (bs *BlockStore) getIdxFile(chunkStart uint64, create bool) (*os.File, error) {
	if f, ok := bs.idxFiles[chunkStart]; ok {
		// 检查文件句柄是否仍然有效
		if _, err := f.Stat(); err == nil {
			return f, nil
		}
		// 句柄无效，关闭并从缓存删除
		f.Close()
		delete(bs.idxFiles, chunkStart)
	}

	_, idxPath := bs.getChunkFiles(chunkStart)

	var f *os.File
	var err error
	if create {
		f, err = os.OpenFile(idxPath, os.O_RDWR|os.O_CREATE, 0644)
	} else {
		f, err = os.OpenFile(idxPath, os.O_RDONLY, 0644)
	}

	if err != nil {
		return nil, err
	}

	bs.idxFiles[chunkStart] = f
	return f, nil
}

// WriteBlock 写入区块数据
// 追加写入dat文件（数据+SHA3哈希），更新idx文件
// 格式：[N字节区块数据][32字节SHA3哈希]
func (bs *BlockStore) WriteBlock(height uint64, data []byte) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	chunkStart := getChunkStart(height)
	pos := height - chunkStart // 在分片内的位置

	// 获取dat文件
	datFile, err := bs.getDatFile(chunkStart, true)
	if err != nil {
		return fmt.Errorf("failed to open dat file: %v", err)
	}

	// 获取idx文件
	idxFile, err := bs.getIdxFile(chunkStart, true)
	if err != nil {
		return fmt.Errorf("failed to open idx file: %v", err)
	}

	// 获取dat文件当前大小（作为写入偏移量）
	datInfo, err := datFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat dat file: %v", err)
	}
	offset := uint64(datInfo.Size())

	// 计算SHA3-256哈希
	hash := sha3.Sum256(data)

	// 追加写入dat文件：数据 + 哈希
	if _, err := datFile.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("failed to seek dat file: %v", err)
	}
	if _, err := datFile.Write(data); err != nil {
		return fmt.Errorf("failed to write block data: %v", err)
	}
	if _, err := datFile.Write(hash[:]); err != nil {
		return fmt.Errorf("failed to write block hash: %v", err)
	}

	// 写入索引条目（Length只记录数据长度，不含哈希）
	entry := IndexEntry{
		Offset: offset,
		Length: uint32(len(data)),
		Height: uint32(height),
	}

	entryBytes := make([]byte, IndexEntrySize)
	binary.BigEndian.PutUint64(entryBytes[0:8], entry.Offset)
	binary.BigEndian.PutUint32(entryBytes[8:12], entry.Length)
	binary.BigEndian.PutUint32(entryBytes[12:16], entry.Height)

	// 定位到索引位置
	idxOffset := int64(pos * IndexEntrySize)
	if _, err := idxFile.Seek(idxOffset, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek idx file: %v", err)
	}
	if _, err := idxFile.Write(entryBytes); err != nil {
		return fmt.Errorf("failed to write index entry: %v", err)
	}

	// 刷新到磁盘
	datFile.Sync()
	idxFile.Sync()

	return nil
}

// ReadBlock 读取区块数据（主网模式，不校验哈希）
// O(1)操作：1次索引seek + 1次数据read
// 主网信任本地数据，跳过哈希校验以最大化速度
func (bs *BlockStore) ReadBlock(height uint64) ([]byte, error) {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	chunkStart := getChunkStart(height)
	pos := height - chunkStart

	// 获取idx文件
	idxFile, err := bs.getIdxFile(chunkStart, false)
	if err != nil {
		return nil, fmt.Errorf("block not found: chunk %d not exist", chunkStart)
	}

	// 读取索引条目
	idxOffset := int64(pos * IndexEntrySize)
	entryBytes := make([]byte, IndexEntrySize)
	if _, err := idxFile.Seek(idxOffset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek idx file: %v", err)
	}
	if _, err := io.ReadFull(idxFile, entryBytes); err != nil {
		return nil, fmt.Errorf("block %d not found in index", height)
	}

	// 解析索引条目
	entry := IndexEntry{
		Offset: binary.BigEndian.Uint64(entryBytes[0:8]),
		Length: binary.BigEndian.Uint32(entryBytes[8:12]),
		Height: binary.BigEndian.Uint32(entryBytes[12:16]),
	}

	// 校验高度
	if entry.Height != uint32(height) {
		return nil, fmt.Errorf("height mismatch: expected %d, got %d", height, entry.Height)
	}

	// 检查长度有效性
	if entry.Length == 0 {
		return nil, fmt.Errorf("block %d has zero length", height)
	}

	// 获取dat文件
	datFile, err := bs.getDatFile(chunkStart, false)
	if err != nil {
		return nil, fmt.Errorf("failed to open dat file: %v", err)
	}

	// 读取区块数据（不读哈希，跳过校验）
	data := make([]byte, entry.Length)
	if _, err := datFile.Seek(int64(entry.Offset), io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek dat file: %v", err)
	}
	if _, err := io.ReadFull(datFile, data); err != nil {
		return nil, fmt.Errorf("failed to read block data: %v", err)
	}

	return data, nil
}

// ReadBlockWithVerify 读取区块数据（带SHA3哈希校验）
// 供History节点使用，公众入口必须可验证
func (bs *BlockStore) ReadBlockWithVerify(height uint64) ([]byte, error) {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	chunkStart := getChunkStart(height)
	pos := height - chunkStart

	// 获取idx文件
	idxFile, err := bs.getIdxFile(chunkStart, false)
	if err != nil {
		return nil, fmt.Errorf("block not found: chunk %d not exist", chunkStart)
	}

	// 读取索引条目
	idxOffset := int64(pos * IndexEntrySize)
	entryBytes := make([]byte, IndexEntrySize)
	if _, err := idxFile.Seek(idxOffset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek idx file: %v", err)
	}
	if _, err := io.ReadFull(idxFile, entryBytes); err != nil {
		return nil, fmt.Errorf("block %d not found in index", height)
	}

	// 解析索引条目
	entry := IndexEntry{
		Offset: binary.BigEndian.Uint64(entryBytes[0:8]),
		Length: binary.BigEndian.Uint32(entryBytes[8:12]),
		Height: binary.BigEndian.Uint32(entryBytes[12:16]),
	}

	// 校验高度
	if entry.Height != uint32(height) {
		return nil, fmt.Errorf("height mismatch: expected %d, got %d", height, entry.Height)
	}

	// 检查长度有效性
	if entry.Length == 0 {
		return nil, fmt.Errorf("block %d has zero length", height)
	}

	// 获取dat文件
	datFile, err := bs.getDatFile(chunkStart, false)
	if err != nil {
		return nil, fmt.Errorf("failed to open dat file: %v", err)
	}

	// 读取区块数据 + 哈希
	dataWithHash := make([]byte, entry.Length+HashSize)
	if _, err := datFile.Seek(int64(entry.Offset), io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek dat file: %v", err)
	}
	if _, err := io.ReadFull(datFile, dataWithHash); err != nil {
		return nil, fmt.Errorf("failed to read block data: %v", err)
	}

	// 分离数据和哈希
	data := dataWithHash[:entry.Length]
	storedHash := dataWithHash[entry.Length:]

	// 校验哈希
	computedHash := sha3.Sum256(data)
	if !bytesEqual(computedHash[:], storedHash) {
		return nil, fmt.Errorf("block %d hash verification failed: data corrupted", height)
	}

	return data, nil
}

// bytesEqual 比较两个字节切片是否相等
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// HasBlock 检查区块是否存在
func (bs *BlockStore) HasBlock(height uint64) bool {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	chunkStart := getChunkStart(height)
	pos := height - chunkStart

	_, idxPath := bs.getChunkFiles(chunkStart)

	// 检查idx文件是否存在
	info, err := os.Stat(idxPath)
	if err != nil {
		return false
	}

	// 检查索引位置是否在文件范围内
	idxOffset := int64(pos * IndexEntrySize)
	if idxOffset+IndexEntrySize > info.Size() {
		return false
	}

	// 读取索引条目检查高度
	idxFile, err := bs.getIdxFile(chunkStart, false)
	if err != nil {
		return false
	}

	entryBytes := make([]byte, IndexEntrySize)
	if _, err := idxFile.Seek(idxOffset, io.SeekStart); err != nil {
		return false
	}
	if _, err := io.ReadFull(idxFile, entryBytes); err != nil {
		return false
	}

	// 检查高度是否匹配
	storedHeight := binary.BigEndian.Uint32(entryBytes[12:16])
	return storedHeight == uint32(height)
}

// GetChunkList 获取所有分片的起始高度列表
func (bs *BlockStore) GetChunkList() ([]uint64, error) {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	entries, err := os.ReadDir(bs.blocksDir)
	if err != nil {
		return nil, err
	}

	chunks := []uint64{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// 解析 chunk_XXXXX.dat 格式
		var chunkStart uint64
		if _, err := fmt.Sscanf(name, "chunk_%d.dat", &chunkStart); err == nil {
			chunks = append(chunks, chunkStart)
		}
	}

	// 排序
	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i] < chunks[j]
	})

	return chunks, nil
}

// PruneChunks 删除旧分片（按天数保留）
// keepBlocks: 保留的区块数量（如17280表示保留1天）
// latestHeight: 当前最新高度
func (bs *BlockStore) PruneChunks(latestHeight uint64, keepBlocks uint64) (int, error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	if latestHeight <= keepBlocks {
		return 0, nil // 没有需要删除的
	}

	minKeepHeight := latestHeight - keepBlocks
	minKeepChunk := getChunkStart(minKeepHeight)

	// 获取所有分片
	entries, err := os.ReadDir(bs.blocksDir)
	if err != nil {
		return 0, err
	}

	deletedCount := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		var chunkStart uint64

		// 解析dat文件
		if _, err := fmt.Sscanf(name, "chunk_%d.dat", &chunkStart); err == nil {
			// 检查是否需要删除
			if chunkStart+ChunkSize <= minKeepChunk {
				// 关闭缓存的文件句柄
				if f, ok := bs.datFiles[chunkStart]; ok {
					f.Close()
					delete(bs.datFiles, chunkStart)
				}

				// 删除dat文件
				datPath := filepath.Join(bs.blocksDir, name)
				if err := os.Remove(datPath); err != nil {
					fmt.Printf("⚠️  Failed to delete %s: %v\n", datPath, err)
				} else {
					deletedCount++
				}
			}
		}

		// 解析idx文件
		if _, err := fmt.Sscanf(name, "chunk_%d.idx", &chunkStart); err == nil {
			if chunkStart+ChunkSize <= minKeepChunk {
				// 关闭缓存的文件句柄
				if f, ok := bs.idxFiles[chunkStart]; ok {
					f.Close()
					delete(bs.idxFiles, chunkStart)
				}

				// 删除idx文件
				idxPath := filepath.Join(bs.blocksDir, name)
				if err := os.Remove(idxPath); err != nil {
					fmt.Printf("⚠️  Failed to delete %s: %v\n", idxPath, err)
				}
			}
		}
	}

	if deletedCount > 0 {
		fmt.Printf("✓ PRUNE: Deleted %d chunk files (kept blocks >= %d)\n", deletedCount, minKeepHeight)
	}

	return deletedCount, nil
}

// GetEarliestBlockHeight 获取最早的区块高度
func (bs *BlockStore) GetEarliestBlockHeight() (uint64, error) {
	chunks, err := bs.GetChunkList()
	if err != nil {
		return 0, err
	}

	if len(chunks) == 0 {
		return 0, fmt.Errorf("no blocks stored")
	}

	// 获取最早分片的idx文件
	chunkStart := chunks[0]
	idxFile, err := bs.getIdxFile(chunkStart, false)
	if err != nil {
		return 0, err
	}

	// 遍历idx文件找到第一个有效条目
	for pos := uint64(0); pos < ChunkSize; pos++ {
		idxOffset := int64(pos * IndexEntrySize)
		entryBytes := make([]byte, IndexEntrySize)

		if _, err := idxFile.Seek(idxOffset, io.SeekStart); err != nil {
			continue
		}
		if _, err := io.ReadFull(idxFile, entryBytes); err != nil {
			break
		}

		height := binary.BigEndian.Uint32(entryBytes[12:16])
		length := binary.BigEndian.Uint32(entryBytes[8:12])

		if height > 0 && length > 0 {
			return uint64(height), nil
		}
	}

	return chunkStart, nil
}

// GetLatestBlockHeight 获取最新的区块高度
func (bs *BlockStore) GetLatestBlockHeight() (uint64, error) {
	chunks, err := bs.GetChunkList()
	if err != nil {
		return 0, err
	}

	if len(chunks) == 0 {
		return 0, nil
	}

	// 获取最新分片的idx文件
	chunkStart := chunks[len(chunks)-1]
	_, idxPath := bs.getChunkFiles(chunkStart)

	info, err := os.Stat(idxPath)
	if err != nil {
		return 0, err
	}

	// 计算idx文件中的条目数量
	entryCount := uint64(info.Size()) / IndexEntrySize
	if entryCount == 0 {
		return 0, nil
	}

	// 从最后一个条目向前查找有效条目
	idxFile, err := bs.getIdxFile(chunkStart, false)
	if err != nil {
		return 0, err
	}

	for pos := int64(entryCount) - 1; pos >= 0; pos-- {
		idxOffset := pos * IndexEntrySize
		entryBytes := make([]byte, IndexEntrySize)

		if _, err := idxFile.Seek(idxOffset, io.SeekStart); err != nil {
			continue
		}
		if _, err := io.ReadFull(idxFile, entryBytes); err != nil {
			continue
		}

		height := binary.BigEndian.Uint32(entryBytes[12:16])
		length := binary.BigEndian.Uint32(entryBytes[8:12])

		if height > 0 && length > 0 {
			return uint64(height), nil
		}
	}

	return 0, nil
}

// GetBlockRange 获取区块范围数据
func (bs *BlockStore) GetBlockRange(fromHeight, toHeight uint64) ([][]byte, error) {
	if fromHeight > toHeight {
		return nil, nil
	}

	blocks := make([][]byte, 0, toHeight-fromHeight+1)

	for height := fromHeight; height <= toHeight; height++ {
		data, err := bs.ReadBlock(height)
		if err != nil {
			// 区块不存在时停止
			break
		}
		blocks = append(blocks, data)
	}

	return blocks, nil
}

// DeleteBlocksAboveHeight 删除指定高度以上的区块
// 用于链重组
func (bs *BlockStore) DeleteBlocksAboveHeight(targetHeight uint64) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	latestHeight, err := bs.GetLatestBlockHeight()
	if err != nil {
		return err
	}

	if latestHeight <= targetHeight {
		return nil
	}

	fmt.Printf("⚠️  FLATFILE DELETE: Deleting blocks from height %d to %d\n", targetHeight+1, latestHeight)

	// 需要清理的分片范围
	targetChunk := getChunkStart(targetHeight)
	latestChunk := getChunkStart(latestHeight)

	// 删除完整分片（targetHeight之后的完整分片）
	for chunkStart := targetChunk + ChunkSize; chunkStart <= latestChunk; chunkStart += ChunkSize {
		datPath, idxPath := bs.getChunkFiles(chunkStart)

		// 关闭文件句柄
		if f, ok := bs.datFiles[chunkStart]; ok {
			f.Close()
			delete(bs.datFiles, chunkStart)
		}
		if f, ok := bs.idxFiles[chunkStart]; ok {
			f.Close()
			delete(bs.idxFiles, chunkStart)
		}

		// 删除文件
		os.Remove(datPath)
		os.Remove(idxPath)
	}

	// 对于targetHeight所在的分片，需要截断
	if targetHeight >= targetChunk {
		pos := targetHeight - targetChunk + 1 // targetHeight之后的位置

		// 截断idx文件
		_, idxPath := bs.getChunkFiles(targetChunk)
		if f, ok := bs.idxFiles[targetChunk]; ok {
			f.Close()
			delete(bs.idxFiles, targetChunk)
		}

		newIdxSize := int64(pos * IndexEntrySize)
		if err := os.Truncate(idxPath, newIdxSize); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to truncate idx file: %v", err)
		}

		// dat文件需要根据idx重新计算大小
		idxFile, err := os.OpenFile(idxPath, os.O_RDONLY, 0644)
		if err == nil {
			defer idxFile.Close()

			// 读取最后一个有效索引条目，计算dat文件应该的大小
			if pos > 0 {
				lastPos := int64((pos - 1) * IndexEntrySize)
				entryBytes := make([]byte, IndexEntrySize)
				if _, err := idxFile.Seek(lastPos, io.SeekStart); err == nil {
					if _, err := io.ReadFull(idxFile, entryBytes); err == nil {
						offset := binary.BigEndian.Uint64(entryBytes[0:8])
						length := binary.BigEndian.Uint32(entryBytes[8:12])
						// dat文件大小 = offset + 数据长度 + 哈希长度(32字节)
						newDatSize := int64(offset) + int64(length) + HashSize

						datPath, _ := bs.getChunkFiles(targetChunk)
						if f, ok := bs.datFiles[targetChunk]; ok {
							f.Close()
							delete(bs.datFiles, targetChunk)
						}
						if err := os.Truncate(datPath, newDatSize); err != nil && !os.IsNotExist(err) {
							return fmt.Errorf("failed to truncate dat file: %v", err)
						}
					}
				}
			}
		}
	}

	fmt.Printf("✓ FLATFILE DELETE: Deleted blocks above height %d\n", targetHeight)
	return nil
}
