// Package fec implements Reed-Solomon forward error correction for the Moonlight streaming protocol.
// This is a Go port of the reed-solomon code from moonlight-common-c.
package fec

import (
	"errors"
	"sync"
)

const (
	// GFBits is the number of bits in the Galois field
	GFBits = 8
	// GFPP is the primitive polynomial for GF(2^8)
	GFPP = "101110001"
	// GFSize is 2^GFBits - 1
	GFSize = (1 << GFBits) - 1
	// DataShardsMax is the maximum number of data + parity shards
	DataShardsMax = 255
)

var (
	// ErrTooManyShards indicates too many shards requested
	ErrTooManyShards = errors.New("too many shards")
	// ErrNotEnoughShards indicates not enough shards to reconstruct
	ErrNotEnoughShards = errors.New("not enough shards for reconstruction")
	// ErrInvalidShardSize indicates invalid shard parameters
	ErrInvalidShardSize = errors.New("invalid shard size")
)

// gf is a Galois field element
type gf = uint8

// Global tables for GF arithmetic
var (
	gfExp      [2 * GFSize]gf
	gfLog      [GFSize + 1]int
	gfInverse  [GFSize + 1]gf
	gfMulTable [(GFSize + 1) * (GFSize + 1)]gf

	initOnce sync.Once
)

// ReedSolomon represents a Reed-Solomon codec
type ReedSolomon struct {
	dataShards   int
	parityShards int
	totalShards  int
	matrix       []gf
	parity       []gf
}

// Init initializes the Reed-Solomon tables. Must be called before using other functions.
func Init() {
	initOnce.Do(func() {
		generateGF()
		initMulTable()
	})
}

// New creates a new Reed-Solomon codec
func New(dataShards, parityShards int) (*ReedSolomon, error) {
	Init()

	totalShards := dataShards + parityShards
	if totalShards > DataShardsMax || dataShards <= 0 || parityShards <= 0 {
		return nil, ErrTooManyShards
	}

	rs := &ReedSolomon{
		dataShards:   dataShards,
		parityShards: parityShards,
		totalShards:  totalShards,
	}

	// Create Vandermonde matrix
	vm := make([]gf, dataShards*totalShards)
	for row := 0; row < totalShards; row++ {
		for col := 0; col < dataShards; col++ {
			if row == col {
				vm[row*dataShards+col] = 1
			} else {
				vm[row*dataShards+col] = 0
			}
		}
	}

	// Extract and invert top submatrix
	top := subMatrix(vm, 0, 0, dataShards, dataShards, totalShards, dataShards)
	if err := invertMatrix(top, dataShards); err != nil {
		return nil, err
	}

	// Multiply to get encoding matrix
	rs.matrix = multiply(vm, totalShards, dataShards, top, dataShards, dataShards)

	// Generate parity rows using Cauchy matrix construction
	for j := 0; j < parityShards; j++ {
		for i := 0; i < dataShards; i++ {
			rs.matrix[(dataShards+j)*dataShards+i] = gfInverse[(parityShards+i)^j]
		}
	}

	// Extract parity matrix
	rs.parity = subMatrix(rs.matrix, dataShards, 0, totalShards, dataShards, totalShards, dataShards)

	return rs, nil
}

// Encode generates parity shards from data shards
func (rs *ReedSolomon) Encode(shards [][]byte) error {
	if len(shards) != rs.totalShards {
		return ErrInvalidShardSize
	}

	blockSize := len(shards[0])
	for _, s := range shards {
		if len(s) != blockSize {
			return ErrInvalidShardSize
		}
	}

	// Generate parity shards
	dataShards := shards[:rs.dataShards]
	parityShards := shards[rs.dataShards:]

	codeSomeShards(rs.parity, dataShards, parityShards, rs.dataShards, rs.parityShards, blockSize)
	return nil
}

// Reconstruct recovers missing data shards using parity
func (rs *ReedSolomon) Reconstruct(shards [][]byte, present []bool) error {
	if len(shards) != rs.totalShards || len(present) != rs.totalShards {
		return ErrInvalidShardSize
	}

	blockSize := 0
	for i, s := range shards {
		if present[i] {
			if blockSize == 0 {
				blockSize = len(s)
			} else if len(s) != blockSize {
				return ErrInvalidShardSize
			}
		}
	}

	if blockSize == 0 {
		return ErrNotEnoughShards
	}

	// Count missing data shards
	var missingData []int
	for i := 0; i < rs.dataShards; i++ {
		if !present[i] {
			missingData = append(missingData, i)
		}
	}

	if len(missingData) == 0 {
		return nil // All data shards present
	}

	// Collect available parity shards
	var availableParity []int
	var parityData [][]byte
	for i := rs.dataShards; i < rs.totalShards && len(availableParity) < len(missingData); i++ {
		if present[i] {
			availableParity = append(availableParity, i-rs.dataShards)
			parityData = append(parityData, shards[i])
		}
	}

	if len(availableParity) < len(missingData) {
		return ErrNotEnoughShards
	}

	// Build decode matrix
	decodeMatrix := make([]gf, rs.dataShards*rs.dataShards)

	subMatrixRow := 0
	subShards := make([][]byte, rs.dataShards)
	missingIdx := 0

	for i := 0; i < rs.dataShards; i++ {
		if missingIdx < len(missingData) && i == missingData[missingIdx] {
			missingIdx++
		} else {
			// Copy row from identity part of matrix
			for c := 0; c < rs.dataShards; c++ {
				decodeMatrix[subMatrixRow*rs.dataShards+c] = rs.matrix[i*rs.dataShards+c]
			}
			subShards[subMatrixRow] = shards[i]
			subMatrixRow++
		}
	}

	// Add parity rows
	for i := 0; i < len(missingData) && subMatrixRow < rs.dataShards; i++ {
		parityRow := availableParity[i]
		j := rs.dataShards + parityRow
		for c := 0; c < rs.dataShards; c++ {
			decodeMatrix[subMatrixRow*rs.dataShards+c] = rs.matrix[j*rs.dataShards+c]
		}
		subShards[subMatrixRow] = parityData[i]
		subMatrixRow++
	}

	// Invert decode matrix
	if err := invertMatrix(decodeMatrix, rs.dataShards); err != nil {
		return err
	}

	// Recover missing data shards
	outputs := make([][]byte, len(missingData))
	for i, idx := range missingData {
		if shards[idx] == nil {
			shards[idx] = make([]byte, blockSize)
		}
		outputs[i] = shards[idx]
		// Copy row for this output
		copy(decodeMatrix[i*rs.dataShards:], decodeMatrix[idx*rs.dataShards:(idx+1)*rs.dataShards])
	}

	codeSomeShards(decodeMatrix, subShards, outputs, rs.dataShards, len(missingData), blockSize)
	return nil
}

// DataShards returns the number of data shards
func (rs *ReedSolomon) DataShards() int {
	return rs.dataShards
}

// ParityShards returns the number of parity shards
func (rs *ReedSolomon) ParityShards() int {
	return rs.parityShards
}

// TotalShards returns the total number of shards
func (rs *ReedSolomon) TotalShards() int {
	return rs.totalShards
}

// GF arithmetic functions

func modnn(x int) gf {
	for x >= GFSize {
		x -= GFSize
		x = (x >> GFBits) + (x & GFSize)
	}
	return gf(x)
}

func generateGF() {
	var mask gf = 1
	gfExp[GFBits] = 0

	// Generate powers of alpha
	for i := 0; i < GFBits; i++ {
		gfExp[i] = mask
		gfLog[gfExp[i]] = i
		if GFPP[i] == '1' {
			gfExp[GFBits] ^= mask
		}
		mask <<= 1
	}

	gfLog[gfExp[GFBits]] = GFBits
	mask = 1 << (GFBits - 1)

	for i := GFBits + 1; i < GFSize; i++ {
		if gfExp[i-1] >= mask {
			gfExp[i] = gfExp[GFBits] ^ ((gfExp[i-1] ^ mask) << 1)
		} else {
			gfExp[i] = gfExp[i-1] << 1
		}
		gfLog[gfExp[i]] = i
	}

	gfLog[0] = GFSize

	// Set extended exp values for fast multiply
	for i := 0; i < GFSize; i++ {
		gfExp[i+GFSize] = gfExp[i]
	}

	// Generate inverse table
	gfInverse[0] = 0
	gfInverse[1] = 1
	for i := 2; i <= GFSize; i++ {
		gfInverse[i] = gfExp[GFSize-gfLog[i]]
	}
}

func initMulTable() {
	for i := 0; i < GFSize+1; i++ {
		for j := 0; j < GFSize+1; j++ {
			gfMulTable[(i<<8)+j] = gfExp[modnn(gfLog[i]+gfLog[j])]
		}
	}

	for j := 0; j < GFSize+1; j++ {
		gfMulTable[j] = 0
		gfMulTable[j<<8] = 0
	}
}

func gfMul(x, y gf) gf {
	return gfMulTable[(int(x)<<8)+int(y)]
}

func addmul(dst, src []gf, c gf) {
	if c == 0 {
		return
	}
	mulcTable := gfMulTable[int(c)<<8:]
	for i := range dst {
		dst[i] ^= mulcTable[src[i]]
	}
}

func mul(dst, src []gf, c gf) {
	if c == 0 {
		for i := range dst {
			dst[i] = 0
		}
		return
	}
	mulcTable := gfMulTable[int(c)<<8:]
	for i := range dst {
		dst[i] = mulcTable[src[i]]
	}
}

func invertMatrix(src []gf, k int) error {
	indxc := make([]int, k)
	indxr := make([]int, k)
	ipiv := make([]int, k)
	idRow := make([]gf, k)

	for col := 0; col < k; col++ {
		var irow, icol int = -1, -1

		// Find pivot
		if ipiv[col] != 1 && src[col*k+col] != 0 {
			irow = col
			icol = col
		} else {
			for row := 0; row < k && icol == -1; row++ {
				if ipiv[row] != 1 {
					for ix := 0; ix < k; ix++ {
						if ipiv[ix] == 0 && src[row*k+ix] != 0 {
							irow = row
							icol = ix
							break
						}
					}
				}
			}
		}

		if icol == -1 {
			return errors.New("singular matrix")
		}

		ipiv[icol]++

		// Swap rows
		if irow != icol {
			for ix := 0; ix < k; ix++ {
				src[irow*k+ix], src[icol*k+ix] = src[icol*k+ix], src[irow*k+ix]
			}
		}

		indxr[col] = irow
		indxc[col] = icol

		pivotRow := src[icol*k : (icol+1)*k]
		c := pivotRow[icol]

		if c == 0 {
			return errors.New("singular matrix")
		}

		if c != 1 {
			c = gfInverse[c]
			pivotRow[icol] = 1
			for ix := 0; ix < k; ix++ {
				pivotRow[ix] = gfMul(c, pivotRow[ix])
			}
		}

		// Reduce other rows
		idRow[icol] = 1
		pivotIsIdentity := true
		for ix := 0; ix < k; ix++ {
			if pivotRow[ix] != idRow[ix] {
				pivotIsIdentity = false
				break
			}
		}

		if !pivotIsIdentity {
			for ix := 0; ix < k; ix++ {
				if ix != icol {
					p := src[ix*k : (ix+1)*k]
					c := p[icol]
					p[icol] = 0
					addmul(p, pivotRow, c)
				}
			}
		}
		idRow[icol] = 0
	}

	// Unscramble columns
	for col := k - 1; col >= 0; col-- {
		if indxr[col] != indxc[col] {
			for row := 0; row < k; row++ {
				src[row*k+indxr[col]], src[row*k+indxc[col]] = src[row*k+indxc[col]], src[row*k+indxr[col]]
			}
		}
	}

	return nil
}

func subMatrix(matrix []gf, rmin, cmin, rmax, cmax, nrows, ncols int) []gf {
	newM := make([]gf, (rmax-rmin)*(cmax-cmin))
	ptr := 0
	for i := rmin; i < rmax; i++ {
		for j := cmin; j < cmax; j++ {
			newM[ptr] = matrix[i*ncols+j]
			ptr++
		}
	}
	return newM
}

func multiply(a []gf, ar, ac int, b []gf, br, bc int) []gf {
	if ac != br {
		return nil
	}
	newM := make([]gf, ar*bc)
	for r := 0; r < ar; r++ {
		for c := 0; c < bc; c++ {
			var tg gf
			for i := 0; i < ac; i++ {
				tg ^= gfMul(a[r*ac+i], b[i*bc+c])
			}
			newM[r*bc+c] = tg
		}
	}
	return newM
}

func codeSomeShards(matrixRows []gf, inputs, outputs [][]byte, dataShards, outputCount, byteCount int) {
	for c := 0; c < dataShards; c++ {
		in := inputs[c]
		for iRow := 0; iRow < outputCount; iRow++ {
			if c == 0 {
				mul(outputs[iRow], in, matrixRows[iRow*dataShards+c])
			} else {
				addmul(outputs[iRow], in, matrixRows[iRow*dataShards+c])
			}
		}
	}
}
