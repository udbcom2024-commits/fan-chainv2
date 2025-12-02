// migrate_to_flatfile.go
// 迁移工具：将LevelDB中的区块数据迁移到Flat File存储
//
// 编译（在node目录下）：
//   go build -o migrate_to_flatfile.exe ./tools/migrate_to_flatfile.go
//
// 用法：
//   ./migrate_to_flatfile.exe <data_dir>
//
// 示例：
//   ./migrate_to_flatfile.exe ./data
//
// 注意：
//   - 运行前必须先停止fan-chain进程
//   - 迁移后原LevelDB区块数据保留（兼容回退）
//   - 迁移完成后可选择清理LevelDB区块数据

package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

const (
	ChunkSize      = 10000
	IndexEntrySize = 16
)

// Block 简化的区块结构（仅用于迁移）
type Block struct {
	Header struct {
		Height uint64 `json:"height"`
	} `json:"header"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法: go run migrate_to_flatfile.go <data_dir>")
		fmt.Println("示例: go run migrate_to_flatfile.go ./data")
		os.Exit(1)
	}

	dataDir := os.Args[1]

	fmt.Println("========================================")
	fmt.Println("  FAN Chain 区块迁移工具")
	fmt.Println("  LevelDB -> Flat File")
	fmt.Println("========================================")
	fmt.Println()

	// 检查数据目录
	dbPath := filepath.Join(dataDir, "blockchain.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Printf("错误: 数据库不存在 %s\n", dbPath)
		os.Exit(1)
	}

	// 打开LevelDB
	fmt.Printf("打开数据库: %s\n", dbPath)
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		fmt.Printf("错误: 无法打开数据库: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// 创建blocks目录
	blocksDir := filepath.Join(dataDir, "blocks")
	if err := os.MkdirAll(blocksDir, 0755); err != nil {
		fmt.Printf("错误: 无法创建blocks目录: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("输出目录: %s\n", blocksDir)

	// 获取最新高度
	latestHeight := uint64(0)
	data, err := db.Get([]byte("meta:latest_height"), nil)
	if err == nil {
		latestHeight = binary.BigEndian.Uint64(data)
	}
	fmt.Printf("最新区块高度: %d\n", latestHeight)

	// 统计LevelDB中的区块数量
	heightPrefix := []byte("h")
	blockPrefix := []byte("b")

	iter := db.NewIterator(util.BytesPrefix(heightPrefix), nil)
	blockCount := 0
	for iter.Next() {
		blockCount++
	}
	iter.Release()
	fmt.Printf("LevelDB区块数量: %d\n", blockCount)

	if blockCount == 0 {
		fmt.Println("没有需要迁移的区块")
		os.Exit(0)
	}

	// 开始迁移
	fmt.Println()
	fmt.Println("开始迁移...")
	startTime := time.Now()

	// 文件句柄缓存
	datFiles := make(map[uint64]*os.File)
	idxFiles := make(map[uint64]*os.File)

	defer func() {
		for _, f := range datFiles {
			f.Close()
		}
		for _, f := range idxFiles {
			f.Close()
		}
	}()

	// 遍历所有高度映射
	iter = db.NewIterator(util.BytesPrefix(heightPrefix), nil)
	migrated := 0
	errors := 0

	for iter.Next() {
		key := iter.Key()
		if len(key) != 9 {
			continue
		}

		height := binary.BigEndian.Uint64(key[1:])
		hashBytes := iter.Value()

		// 获取区块数据
		blockKey := append(blockPrefix, hashBytes...)
		blockData, err := db.Get(blockKey, nil)
		if err != nil {
			fmt.Printf("⚠️  区块 #%d 数据缺失\n", height)
			errors++
			continue
		}

		// 解析区块验证高度
		var block Block
		if err := json.Unmarshal(blockData, &block); err != nil {
			fmt.Printf("⚠️  区块 #%d 解析失败: %v\n", height, err)
			errors++
			continue
		}

		if block.Header.Height != height {
			fmt.Printf("⚠️  区块高度不匹配: 期望 %d, 实际 %d\n", height, block.Header.Height)
			errors++
			continue
		}

		// 计算分片
		chunkStart := (height / ChunkSize) * ChunkSize
		pos := height - chunkStart

		// 获取或创建dat文件
		datFile, ok := datFiles[chunkStart]
		if !ok {
			datPath := filepath.Join(blocksDir, fmt.Sprintf("chunk_%d.dat", chunkStart))
			datFile, err = os.OpenFile(datPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
			if err != nil {
				fmt.Printf("错误: 无法创建 %s: %v\n", datPath, err)
				errors++
				continue
			}
			datFiles[chunkStart] = datFile
		}

		// 获取或创建idx文件
		idxFile, ok := idxFiles[chunkStart]
		if !ok {
			idxPath := filepath.Join(blocksDir, fmt.Sprintf("chunk_%d.idx", chunkStart))
			idxFile, err = os.OpenFile(idxPath, os.O_RDWR|os.O_CREATE, 0644)
			if err != nil {
				fmt.Printf("错误: 无法创建 %s: %v\n", idxPath, err)
				errors++
				continue
			}
			idxFiles[chunkStart] = idxFile
		}

		// 获取当前dat文件大小
		info, err := datFile.Stat()
		if err != nil {
			errors++
			continue
		}
		offset := uint64(info.Size())

		// 写入区块数据
		if _, err := datFile.Write(blockData); err != nil {
			fmt.Printf("错误: 写入区块 #%d 失败: %v\n", height, err)
			errors++
			continue
		}

		// 写入索引
		entryBytes := make([]byte, IndexEntrySize)
		binary.BigEndian.PutUint64(entryBytes[0:8], offset)
		binary.BigEndian.PutUint32(entryBytes[8:12], uint32(len(blockData)))
		binary.BigEndian.PutUint32(entryBytes[12:16], uint32(height))

		idxOffset := int64(pos * IndexEntrySize)
		if _, err := idxFile.WriteAt(entryBytes, idxOffset); err != nil {
			fmt.Printf("错误: 写入索引 #%d 失败: %v\n", height, err)
			errors++
			continue
		}

		migrated++

		// 进度报告
		if migrated%1000 == 0 {
			fmt.Printf("已迁移: %d / %d 区块\n", migrated, blockCount)
		}
	}
	iter.Release()

	// 同步所有文件
	for _, f := range datFiles {
		f.Sync()
	}
	for _, f := range idxFiles {
		f.Sync()
	}

	elapsed := time.Since(startTime)

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  迁移完成")
	fmt.Println("========================================")
	fmt.Printf("成功迁移: %d 区块\n", migrated)
	fmt.Printf("错误数量: %d\n", errors)
	fmt.Printf("耗时: %v\n", elapsed)
	fmt.Printf("速度: %.2f 区块/秒\n", float64(migrated)/elapsed.Seconds())
	fmt.Println()

	// 统计生成的文件
	entries, _ := os.ReadDir(blocksDir)
	datCount := 0
	var totalSize int64
	for _, entry := range entries {
		if !entry.IsDir() {
			info, _ := entry.Info()
			totalSize += info.Size()
			if filepath.Ext(entry.Name()) == ".dat" {
				datCount++
			}
		}
	}
	fmt.Printf("生成文件: %d 个分片 (%d 文件)\n", datCount, len(entries))
	fmt.Printf("总大小: %.2f MB\n", float64(totalSize)/(1024*1024))
	fmt.Println()
	fmt.Println("注意: LevelDB中的区块数据已保留，可以选择手动清理")
}
