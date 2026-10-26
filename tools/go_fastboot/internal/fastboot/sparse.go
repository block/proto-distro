package fastboot

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// Sparse image format constants.
const (
	sparseHeaderMagic = 0xed26ff3a
	sparseHeaderSize  = 28
	chunkHeaderSize   = 12
)

const rawBlockSize = 4096

// Chunk types.
const (
	chunkTypeRaw      = 0xCAC1
	chunkTypeFill     = 0xCAC2
	chunkTypeDontCare = 0xCAC3
	chunkTypeCRC32    = 0xCAC4
)

// SparseHeader represents the sparse image file header.
type SparseHeader struct {
	Magic         uint32
	MajorVersion  uint16
	MinorVersion  uint16
	FileHdrSize   uint16
	ChunkHdrSize  uint16
	BlockSize     uint32
	TotalBlocks   uint32
	TotalChunks   uint32
	ImageChecksum uint32
}

// ChunkHeader represents a sparse chunk header.
type ChunkHeader struct {
	ChunkType   uint16
	Reserved    uint16
	ChunkBlocks uint32
	TotalSize   uint32 // Including chunk header.
}

// SparseImage owns a validated file and metadata, not its payloads.
type SparseImage struct {
	Header     SparseHeader
	Chunks     []SparseChunk
	file       *os.File
	sourceSize int64
	raw        bool
}

// SparseChunk describes source bytes without reading them into memory.
type SparseChunk struct {
	Header     ChunkHeader
	DataOffset int64
	DataSize   int64 // Actual source bytes; the final virtual RAW block may need padding.
}

// IsSparseImage checks the magic, treating only short files as nonsparse.
func IsSparseImage(path string) (bool, error) {
	f, _, err := openImageFile(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	var magic uint32
	if err := binary.Read(f, binary.LittleEndian, &magic); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		return false, err
	}
	return magic == sparseHeaderMagic, nil
}

func openImageFile(path string) (*os.File, int64, error) {
	// Reject existing named pipes before a blocking open; still check the descriptor.
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("stat image: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("image must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("opening image: %w", err)
	}
	info, err = f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("stat image: %w", err)
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, 0, fmt.Errorf("image must be a regular file")
	}
	return f, info.Size(), nil
}

func validateSparseHeader(header SparseHeader) error {
	if header.Magic != sparseHeaderMagic || header.MajorVersion != 1 {
		return fmt.Errorf("unsupported sparse magic or major version")
	}
	if header.FileHdrSize < sparseHeaderSize || header.ChunkHdrSize < chunkHeaderSize {
		return fmt.Errorf("sparse header sizes are too small")
	}
	if header.BlockSize == 0 || header.BlockSize%4 != 0 || header.TotalBlocks == 0 {
		return fmt.Errorf("invalid sparse block size or block count")
	}
	if uint64(header.BlockSize)*uint64(header.TotalBlocks) > math.MaxInt64 {
		return fmt.Errorf("sparse logical size exceeds int64")
	}
	return nil
}

// sparsePayloadSize validates a chunk's type and block count before computing bytes.
func sparsePayloadSize(header ChunkHeader, blockSize uint32) (int64, error) {
	if header.ChunkType == chunkTypeCRC32 {
		if header.ChunkBlocks != 0 {
			return 0, fmt.Errorf("CRC32 chunk must have zero blocks")
		}
		return 4, nil
	}
	if header.ChunkBlocks == 0 {
		return 0, fmt.Errorf("data chunk must have positive blocks")
	}
	switch header.ChunkType {
	case chunkTypeRaw:
		payload := uint64(header.ChunkBlocks) * uint64(blockSize)
		if payload > math.MaxUint32-chunkHeaderSize {
			return 0, fmt.Errorf("RAW payload exceeds chunk size field")
		}
		return int64(payload), nil
	case chunkTypeFill:
		return 4, nil
	case chunkTypeDontCare:
		return 0, nil
	default:
		return 0, fmt.Errorf("unknown sparse chunk type 0x%x", header.ChunkType)
	}
}

// OpenSparseImage validates every source span before retaining chunk descriptors.
// Header extensions and minor versions are supported; CRC values are not verified.
func OpenSparseImage(path string) (_ *SparseImage, err error) {
	f, size, err := openImageFile(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			f.Close()
		}
	}()
	image := &SparseImage{file: f, sourceSize: size}
	if err := binary.Read(f, binary.LittleEndian, &image.Header); err != nil {
		return nil, fmt.Errorf("reading sparse header: %w", err)
	}
	if err := validateSparseHeader(image.Header); err != nil {
		return nil, err
	}
	header := image.Header
	position := int64(header.FileHdrSize)
	if position > size || int64(header.TotalChunks) > (size-position)/int64(header.ChunkHdrSize) {
		return nil, fmt.Errorf("sparse headers exceed file size")
	}
	var blocks uint64
	var fixed [chunkHeaderSize]byte
	for i := uint32(0); i < header.TotalChunks; i++ {
		if int64(header.ChunkHdrSize) > size-position {
			return nil, fmt.Errorf("chunk %d header exceeds file size", i)
		}
		if _, err := f.ReadAt(fixed[:], position); err != nil {
			return nil, fmt.Errorf("reading chunk %d header: %w", i, err)
		}
		var chunk SparseChunk
		if _, err := binary.Decode(fixed[:], binary.LittleEndian, &chunk.Header); err != nil {
			return nil, err
		}
		if chunk.Header.TotalSize < uint32(header.ChunkHdrSize) || int64(chunk.Header.TotalSize) > size-position {
			return nil, fmt.Errorf("chunk %d has invalid source span", i)
		}
		payload, err := sparsePayloadSize(chunk.Header, header.BlockSize)
		if err != nil {
			return nil, fmt.Errorf("chunk %d: %w", i, err)
		}
		chunk.DataSize = int64(chunk.Header.TotalSize) - int64(header.ChunkHdrSize)
		if chunk.DataSize != payload {
			return nil, fmt.Errorf("chunk %d payload size mismatch", i)
		}
		chunk.DataOffset = position + int64(header.ChunkHdrSize)
		blocks += uint64(chunk.Header.ChunkBlocks)
		if blocks > uint64(header.TotalBlocks) {
			return nil, fmt.Errorf("chunk %d exceeds declared logical blocks", i)
		}
		image.Chunks = append(image.Chunks, chunk)
		position += int64(chunk.Header.TotalSize)
	}
	if blocks != uint64(header.TotalBlocks) || position != size {
		return nil, fmt.Errorf("sparse block count or final file span mismatch")
	}
	return image, nil
}

// openRawImage describes a raw source as virtual RAW chunks with 4096-byte blocks.
func openRawImage(path string) (*SparseImage, error) {
	f, size, err := openImageFile(path)
	if err != nil {
		return nil, err
	}
	blocks, err := rawImageBlockCount(size)
	if err != nil {
		f.Close()
		return nil, err
	}
	image := &SparseImage{
		Header: SparseHeader{
			Magic: sparseHeaderMagic, MajorVersion: 1,
			FileHdrSize: sparseHeaderSize, ChunkHdrSize: chunkHeaderSize,
			BlockSize: rawBlockSize, TotalBlocks: blocks,
		},
		file: f, sourceSize: size, raw: true,
	}
	const maxChunkBlocks = (math.MaxUint32 - chunkHeaderSize) / rawBlockSize
	var offset int64
	for remaining := int64(blocks); remaining > 0; {
		count := min(remaining, int64(maxChunkBlocks))
		payload := count * rawBlockSize
		image.Chunks = append(image.Chunks, SparseChunk{
			Header: ChunkHeader{
				ChunkType: chunkTypeRaw, ChunkBlocks: uint32(count),
				TotalSize: uint32(chunkHeaderSize + payload),
			},
			DataOffset: offset, DataSize: min(payload, size-offset),
		})
		offset += payload
		remaining -= count
	}
	image.Header.TotalChunks = uint32(len(image.Chunks))
	return image, nil
}

func rawImageBlockCount(size int64) (uint32, error) {
	if size <= 0 || size > int64(math.MaxUint32)*rawBlockSize {
		return 0, fmt.Errorf("raw image has invalid sparse block count")
	}
	return uint32((size-1)/rawBlockSize + 1), nil
}

// Close closes the owned source file.
func (s *SparseImage) Close() error {
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

// SplitInfo retains only metadata. WireSize includes all headers and skip chunks.
type SplitInfo struct {
	Chunks      []SparseChunk
	StartBlock  uint32
	TotalBlocks uint32
	WireSize    int64
}

// CalculateSplits greedily plans bounded transfers without reading payloads.
// Every transfer retains the original logical geometry and preserves DONT_CARE.
func (s *SparseImage) CalculateSplits(maxSize int64) ([]SplitInfo, error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("download size must be positive")
	}
	if err := validateSparseHeader(s.Header); err != nil {
		return nil, err
	}
	var splits []SplitInfo
	current := SplitInfo{WireSize: sparseHeaderSize}
	var position uint32
	finish := func() error {
		if position < s.Header.TotalBlocks {
			current.WireSize += chunkHeaderSize
		}
		if current.WireSize > maxSize {
			return fmt.Errorf("split exceeds download size")
		}
		if err := s.validateSplit(current); err != nil {
			return err
		}
		splits = append(splits, current)
		current = SplitInfo{StartBlock: position, WireSize: sparseHeaderSize}
		if position > 0 {
			current.WireSize += chunkHeaderSize
		}
		return nil
	}
	for _, source := range s.Chunks {
		payload, err := sparsePayloadSize(source.Header, s.Header.BlockSize)
		if err != nil {
			return nil, err
		}
		if source.Header.ChunkType == chunkTypeCRC32 {
			continue
		}
		if source.Header.ChunkBlocks > s.Header.TotalBlocks-position {
			return nil, fmt.Errorf("source chunks exceed logical blocks")
		}
		source.Header.Reserved = 0
		source.Header.TotalSize = uint32(chunkHeaderSize + payload)
		for source.Header.ChunkBlocks > 0 {
			trailing := int64(0)
			if position+source.Header.ChunkBlocks < s.Header.TotalBlocks {
				trailing = chunkHeaderSize
			}
			if int64(source.Header.TotalSize) <= maxSize-current.WireSize-trailing {
				current.Chunks = append(current.Chunks, source)
				current.TotalBlocks += source.Header.ChunkBlocks
				current.WireSize += int64(source.Header.TotalSize)
				position += source.Header.ChunkBlocks
				break
			}
			// A partial RAW always needs a trailing skip; never divide a FILL extent.
			available := maxSize - current.WireSize - 2*chunkHeaderSize
			if source.Header.ChunkType == chunkTypeRaw && available >= int64(s.Header.BlockSize) {
				count := uint32(min(available/int64(s.Header.BlockSize), int64(source.Header.ChunkBlocks)))
				bytes := int64(count) * int64(s.Header.BlockSize)
				fragment := source
				fragment.Header.ChunkBlocks = count
				fragment.Header.TotalSize = uint32(chunkHeaderSize + bytes)
				fragment.DataSize = min(bytes, source.DataSize)
				current.Chunks = append(current.Chunks, fragment)
				current.TotalBlocks += count
				current.WireSize += int64(fragment.Header.TotalSize)
				position += count
				source.Header.ChunkBlocks -= count
				source.Header.TotalSize -= uint32(bytes)
				source.DataOffset += bytes
				source.DataSize -= fragment.DataSize
			}
			if len(current.Chunks) == 0 {
				return nil, fmt.Errorf("download size %d cannot hold chunk at block %d", maxSize, position)
			}
			if err := finish(); err != nil {
				return nil, err
			}
		}
	}
	if position != s.Header.TotalBlocks {
		return nil, fmt.Errorf("source chunks do not cover logical blocks")
	}
	if len(current.Chunks) > 0 {
		if err := finish(); err != nil {
			return nil, err
		}
	}
	return splits, nil
}

// validateSplit checks all buffer sizes and source spans before allocation or I/O.
func (s *SparseImage) validateSplit(split SplitInfo) error {
	if err := validateSparseHeader(s.Header); err != nil {
		return err
	}
	end := uint64(split.StartBlock) + uint64(split.TotalBlocks)
	if len(split.Chunks) == 0 || split.TotalBlocks == 0 || end > uint64(s.Header.TotalBlocks) {
		return fmt.Errorf("invalid split block range")
	}
	wire := int64(sparseHeaderSize)
	chunks := uint64(len(split.Chunks))
	if split.StartBlock > 0 {
		wire += chunkHeaderSize
		chunks++
	}
	if end < uint64(s.Header.TotalBlocks) {
		wire += chunkHeaderSize
		chunks++
	}
	if chunks > math.MaxUint32 {
		return fmt.Errorf("split exceeds chunk count field")
	}
	var blocks uint64
	for _, chunk := range split.Chunks {
		payload, err := sparsePayloadSize(chunk.Header, s.Header.BlockSize)
		if err != nil {
			return err
		}
		if chunk.Header.ChunkType == chunkTypeCRC32 || int64(chunk.Header.TotalSize) != chunkHeaderSize+payload {
			return fmt.Errorf("invalid split chunk header")
		}
		if chunk.DataOffset < 0 || chunk.DataOffset > s.sourceSize || chunk.DataSize < 0 || chunk.DataSize > s.sourceSize-chunk.DataOffset || chunk.DataSize > payload {
			return fmt.Errorf("invalid split source span")
		}
		blocks += uint64(chunk.Header.ChunkBlocks)
		if blocks > uint64(split.TotalBlocks) {
			return fmt.Errorf("split chunks exceed block range")
		}
		if chunk.DataSize != payload {
			if !s.raw || chunk.Header.ChunkType != chunkTypeRaw || chunk.DataSize == 0 || payload-chunk.DataSize >= int64(s.Header.BlockSize) || chunk.DataOffset+chunk.DataSize != s.sourceSize || uint64(split.StartBlock)+blocks != uint64(s.Header.TotalBlocks) {
				return fmt.Errorf("only the final virtual RAW block may be padded")
			}
		}
		if int64(chunk.Header.TotalSize) > math.MaxInt64-wire {
			return fmt.Errorf("split wire size exceeds int64")
		}
		wire += int64(chunk.Header.TotalSize)
	}
	if blocks != uint64(split.TotalBlocks) || wire != split.WireSize {
		return fmt.Errorf("split blocks or wire size mismatch")
	}
	return nil
}

// BuildSplitImage encodes directly into dst and reads only this part's payloads.
// The returned slice remains valid only until dst is reused for the next part.
func (s *SparseImage) BuildSplitImage(split SplitInfo, dst []byte) ([]byte, error) {
	if err := s.validateSplit(split); err != nil {
		return nil, err
	}
	if split.WireSize > int64(int(^uint(0)>>1)) {
		return nil, fmt.Errorf("split exceeds addressable buffer size")
	}
	if s.file == nil {
		return nil, fmt.Errorf("sparse source is not open")
	}
	if int64(cap(dst)) < split.WireSize {
		dst = make([]byte, int(split.WireSize))
	} else {
		dst = dst[:int(split.WireSize)]
	}
	trailing := s.Header.TotalBlocks - split.StartBlock - split.TotalBlocks
	count := uint32(len(split.Chunks))
	if split.StartBlock > 0 {
		count++
	}
	if trailing > 0 {
		count++
	}
	header := SparseHeader{
		Magic: sparseHeaderMagic, MajorVersion: 1,
		FileHdrSize: sparseHeaderSize, ChunkHdrSize: chunkHeaderSize,
		BlockSize: s.Header.BlockSize, TotalBlocks: s.Header.TotalBlocks, TotalChunks: count,
	}
	position, err := binary.Encode(dst, binary.LittleEndian, header)
	if err != nil {
		return nil, err
	}
	putChunk := func(header ChunkHeader) error {
		n, err := binary.Encode(dst[position:], binary.LittleEndian, header)
		position += n
		return err
	}
	if split.StartBlock > 0 {
		if err := putChunk(ChunkHeader{ChunkType: chunkTypeDontCare, ChunkBlocks: split.StartBlock, TotalSize: chunkHeaderSize}); err != nil {
			return nil, err
		}
	}
	for i, chunk := range split.Chunks {
		if err := putChunk(chunk.Header); err != nil {
			return nil, err
		}
		payload := int(chunk.Header.TotalSize) - chunkHeaderSize
		if chunk.DataSize > 0 {
			if _, err := s.file.ReadAt(dst[position:position+int(chunk.DataSize)], chunk.DataOffset); err != nil {
				return nil, fmt.Errorf("reading split chunk %d: %w", i, err)
			}
		}
		clear(dst[position+int(chunk.DataSize) : position+payload])
		position += payload
	}
	if trailing > 0 {
		if err := putChunk(ChunkHeader{ChunkType: chunkTypeDontCare, ChunkBlocks: trailing, TotalSize: chunkHeaderSize}); err != nil {
			return nil, err
		}
	}
	if position != len(dst) {
		return nil, fmt.Errorf("encoded split size does not match plan")
	}
	return dst, nil
}

// writeSparseHeader writes a normalized sparse image header.
func writeSparseHeader(buf io.Writer, blockSize, totalBlocks, totalChunks uint32) error {
	header := SparseHeader{
		Magic: sparseHeaderMagic, MajorVersion: 1,
		FileHdrSize: sparseHeaderSize, ChunkHdrSize: chunkHeaderSize,
		BlockSize: blockSize, TotalBlocks: totalBlocks, TotalChunks: totalChunks,
	}
	return binary.Write(buf, binary.LittleEndian, &header)
}

// writeChunkHeader writes a normalized chunk header.
func writeChunkHeader(buf io.Writer, chunkType uint16, chunkBlocks, dataSize uint32) error {
	if dataSize > math.MaxUint32-chunkHeaderSize {
		return fmt.Errorf("chunk payload exceeds size field")
	}
	header := ChunkHeader{
		ChunkType: chunkType, ChunkBlocks: chunkBlocks, TotalSize: chunkHeaderSize + dataSize,
	}
	return binary.Write(buf, binary.LittleEndian, &header)
}
