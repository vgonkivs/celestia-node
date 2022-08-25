package p2p

import p2p_pb "github.com/celestiaorg/celestia-node/header/p2p/pb"

// MarshalExtendedHeaderRequest serializes the given ExtendedHeaderRequest to bytes using protobuf.
// Paired with UnmarshalExtendedHeaderRequest.
func MarshalExtendedHeaderRequest(in *ExtendedHeaderRequest) ([]byte, error) {
	out := &p2p_pb.ExtendedHeaderRequest{
		Data:   &p2p_pb.ExtendedHeaderRequest_Origin{Origin: in.Origin},
		Amount: in.Amount,
	}
	return out.Marshal()
}

// UnmarshalExtendedHeaderRequest deserializes given data into a new ExtendedHeader using protobuf.
// Paired with MarshalExtendedHeaderRequest.
func UnmarshalExtendedHeaderRequest(data []byte) (*ExtendedHeaderRequest, error) {
	in := &p2p_pb.ExtendedHeaderRequest{}
	err := in.Unmarshal(data)
	if err != nil {
		return nil, err
	}
	return &ExtendedHeaderRequest{
		Origin: in.GetOrigin(),
		Amount: in.Amount,
	}, nil
}
