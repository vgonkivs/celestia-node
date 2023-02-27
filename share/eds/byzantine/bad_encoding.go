package byzantine

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/celestiaorg/celestia-app/pkg/appconsts"
	"github.com/celestiaorg/celestia-app/pkg/wrapper"
	"github.com/celestiaorg/rsmt2d"

	"github.com/celestiaorg/celestia-node/fraud"
	"github.com/celestiaorg/celestia-node/header"
	pb "github.com/celestiaorg/celestia-node/share/eds/byzantine/pb"
	"github.com/celestiaorg/celestia-node/share/ipld"
)

func init() {
	fraud.Register(&BadEncodingProof{})
}

const (
	BadEncoding fraud.ProofType = "badencoding"
)

type BadEncodingProof struct {
	headerHash  []byte
	BlockHeight uint64
	// ShareWithProof contains all shares from row or col.
	// Shares that did not pass verification in rsmt2d will be nil.
	// For non-nil shares MerkleProofs are computed.
	Shares []*ShareWithProof
	// Index represents the row/col index where ErrByzantineRow/ErrByzantineColl occurred.
	Index uint32
	// Axis represents the axis that verification failed on.
	Axis rsmt2d.Axis
}

// CreateBadEncodingProof creates a new Bad Encoding Fraud Proof that should be propagated through
// network. The fraud proof will contain shares that did not pass verification and their relevant
// Merkle proofs.
func CreateBadEncodingProof(
	hash []byte,
	height uint64,
	errByzantine *ErrByzantine,
) fraud.Proof {

	return &BadEncodingProof{
		headerHash:  hash,
		BlockHeight: height,
		Shares:      errByzantine.Shares,
		Index:       errByzantine.Index,
		Axis:        errByzantine.Axis,
	}
}

// Type returns type of fraud proof.
func (p *BadEncodingProof) Type() fraud.ProofType {
	return BadEncoding
}

// HeaderHash returns block hash.
func (p *BadEncodingProof) HeaderHash() []byte {
	return p.headerHash
}

// Height returns block height.
func (p *BadEncodingProof) Height() uint64 {
	return p.BlockHeight
}

// MarshalBinary converts BadEncodingProof to binary.
func (p *BadEncodingProof) MarshalBinary() ([]byte, error) {
	shares := make([]*pb.Share, 0, len(p.Shares))
	for _, share := range p.Shares {
		shares = append(shares, share.ShareWithProofToProto())
	}

	badEncodingFraudProof := pb.BadEncoding{
		HeaderHash: p.headerHash,
		Height:     p.BlockHeight,
		Shares:     shares,
		Index:      p.Index,
		Axis:       pb.Axis(p.Axis),
	}
	return badEncodingFraudProof.Marshal()
}

// UnmarshalBinary converts binary to BadEncodingProof.
func (p *BadEncodingProof) UnmarshalBinary(data []byte) error {
	in := pb.BadEncoding{}
	if err := in.Unmarshal(data); err != nil {
		return err
	}
	befp := &BadEncodingProof{
		headerHash:  in.HeaderHash,
		BlockHeight: in.Height,
		Shares:      ProtoToShare(in.Shares),
		Index:       in.Index,
		Axis:        rsmt2d.Axis(in.Axis),
	}

	*p = *befp

	return nil
}

// Validate ensures that fraud proof is correct.
// Validate checks that provided Merkle Proofs correspond to the shares,
// rebuilds bad row or col from received shares, computes Merkle Root
// and compares it with block's Merkle Root.
func (p *BadEncodingProof) Validate(header *header.ExtendedHeader) error {
	if header.Height() != int64(p.BlockHeight) {
		return errors.New("fraud: incorrect block height")
	}
	merkleRowRoots := header.DAH.RowsRoots
	merkleColRoots := header.DAH.ColumnRoots
	if len(merkleRowRoots) != len(merkleColRoots) {
		// NOTE: This should never happen as callers of this method should not feed it with a
		// malformed extended header.
		panic(fmt.Sprintf(
			"fraud: invalid extended header: length of row and column roots do not match. (rowRoots=%d) (colRoots=%d)",
			len(merkleRowRoots),
			len(merkleColRoots)),
		)
	}
	if int(p.Index) >= len(merkleRowRoots) {
		return fmt.Errorf("fraud: invalid proof: index out of bounds (%d >= %d)", int(p.Index), len(merkleRowRoots))
	}
	if len(merkleRowRoots) != len(p.Shares) {
		return fmt.Errorf("fraud: invalid proof: incorrect number of shares %d != %d", len(p.Shares), len(merkleRowRoots))
	}

	root := merkleRowRoots[p.Index]
	if p.Axis == rsmt2d.Col {
		root = merkleColRoots[p.Index]
	}

	// verify that Merkle proofs correspond to particular shares.
	shares := make([][]byte, len(merkleRowRoots))
	for index, share := range p.Shares {
		if share == nil {
			continue
		}
		// validate inclusion of the share into one of the DAHeader roots
		if ok := share.Validate(ipld.MustCidFromNamespacedSha256(root)); !ok {
			return fmt.Errorf("fraud: invalid proof: incorrect share received at index %d", index)
		}
		// NMTree commits the additional namespace while rsmt2d does not know about, so we trim it
		// this is ugliness from NMTWrapper that we have to embrace ¯\_(ツ)_/¯
		shares[index] = share.Share[ipld.NamespaceSize:]
	}

	odsWidth := uint64(len(merkleRowRoots) / 2)
	codec := appconsts.DefaultCodec()

	// rebuild a row or col.
	rebuiltShares, err := codec.Decode(shares)
	if err != nil {
		return err
	}
	rebuiltExtendedShares, err := codec.Encode(rebuiltShares[0:odsWidth])
	if err != nil {
		return err
	}
	copy(rebuiltShares[odsWidth:], rebuiltExtendedShares)

	tree := wrapper.NewErasuredNamespacedMerkleTree(odsWidth, uint(p.Index))
	for _, share := range rebuiltShares {
		tree.Push(share)
	}

	// comparing rebuilt Merkle Root of bad row/col with respective Merkle Root of row/col from block.
	if bytes.Equal(tree.Root(), root) {
		return errors.New("fraud: invalid proof: recomputed Merkle root matches the DAH's row/column root")
	}

	return nil
}
