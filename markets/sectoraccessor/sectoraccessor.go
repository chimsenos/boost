package sectoraccessor

import (
	"context"
	"fmt"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"
	"golang.org/x/xerrors"
	"io"
	"os"

	"github.com/filecoin-project/boost-gfm/retrievalmarket"
	"github.com/filecoin-project/dagstore/mount"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	logging "github.com/ipfs/go-log/v2"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/api/v1api"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/markets/dagstore"
	"github.com/filecoin-project/lotus/node/modules/dtypes"
	"github.com/filecoin-project/lotus/storage/sealer"
	"github.com/filecoin-project/lotus/storage/sectorblocks"
)

var log = logging.Logger("sectoraccessor")

type sectorAccessor struct {
	maddr address.Address
	secb  sectorblocks.SectorBuilder
	pp    sealer.PieceProvider
	full  v1api.FullNode
}

var _ retrievalmarket.SectorAccessor = (*sectorAccessor)(nil)

func NewSectorAccessor(maddr dtypes.MinerAddress, secb sectorblocks.SectorBuilder, pp sealer.PieceProvider, full v1api.FullNode) dagstore.SectorAccessor {
	return &sectorAccessor{address.Address(maddr), secb, pp, full}
}

func (sa *sectorAccessor) UnsealSector(ctx context.Context, sectorID abi.SectorNumber, pieceOffset abi.UnpaddedPieceSize, length abi.UnpaddedPieceSize) (io.ReadCloser, error) {
	return sa.UnsealSectorAt(ctx, sectorID, pieceOffset, length)
}

func (sa *sectorAccessor) UnsealSectorAt(ctx context.Context, sectorID abi.SectorNumber, pieceOffset abi.UnpaddedPieceSize, length abi.UnpaddedPieceSize) (mount.Reader, error) {
	si, err := sa.sectorsStatus(ctx, sectorID, false)
	if err != nil {
		return nil, err
	}

	piece := si.Pieces[0]
	if pieceOffset > 0 && len(si.Pieces) > 1 {
		piece = si.Pieces[1]
	}

	url, ok := os.LookupEnv("MINIO_CAR_PATH")
	if !ok {
		return nil, xerrors.New("place setting env for MINIO_CAR_PATH")
	}

	url = fmt.Sprintf("%s/%s.car", url, piece.Piece.PieceCID.String())

	if r, err := g.Client().Get(ctx, url); err != nil {
		return nil, err
	} else {
		defer func(r *gclient.Response) {
			var err = r.Close()
			if err != nil {
				log.Debugf("http client close error: %s", err.Error())
			}
		}(r)
		if r.StatusCode == 404 {
			return nil, xerrors.New("not fond car")
		} else if r.StatusCode == 401 {
			return nil, xerrors.New("no permission")
		}
		data := mount.BytesMount{Bytes: r.ReadAll()}
		return data.Fetch(ctx)
	}
}

//func (sa *sectorAccessor) UnsealSectorAt(ctx context.Context, sectorID abi.SectorNumber, pieceOffset abi.UnpaddedPieceSize, length abi.UnpaddedPieceSize) (mount.Reader, error) {
//	log.Debugf("get sector %d, pieceOffset %d, length %d", sectorID, pieceOffset, length)
//	si, err := sa.sectorsStatus(ctx, sectorID, false)
//	if err != nil {
//		return nil, err
//	}
//
//	mid, err := address.IDFromAddress(sa.maddr)
//	if err != nil {
//		return nil, err
//	}
//
//	ref := storiface.SectorRef{
//		ID: abi.SectorID{
//			Miner:  abi.ActorID(mid),
//			Number: sectorID,
//		},
//		ProofType: si.SealProof,
//	}
//
//	var commD cid.Cid
//	if si.CommD != nil {
//		commD = *si.CommD
//	}
//
//	// Get a reader for the piece, unsealing the piece if necessary
//	log.Debugf("read piece in sector %d, pieceOffset %d, length %d from miner %d", sectorID, pieceOffset, length, mid)
//	r, unsealed, err := sa.pp.ReadPiece(ctx, ref, storiface.UnpaddedByteIndex(pieceOffset), length, si.Ticket.Value, commD)
//	if err != nil {
//		return nil, xerrors.Errorf("failed to unseal piece from sector %d: %w", sectorID, err)
//	}
//	_ = unsealed // todo: use
//
//	return r, nil
//}

func (sa *sectorAccessor) IsUnsealed(ctx context.Context, sectorID abi.SectorNumber, offset abi.UnpaddedPieceSize, length abi.UnpaddedPieceSize) (bool, error) {
	return true, nil
}

//func (sa *sectorAccessor) IsUnsealed(ctx context.Context, sectorID abi.SectorNumber, offset abi.UnpaddedPieceSize, length abi.UnpaddedPieceSize) (bool, error) {
//	si, err := sa.sectorsStatus(ctx, sectorID, true)
//	if err != nil {
//		return false, xerrors.Errorf("failed to get sector info: %w", err)
//	}
//
//	mid, err := address.IDFromAddress(sa.maddr)
//	if err != nil {
//		return false, err
//	}
//
//	ref := storiface.SectorRef{
//		ID: abi.SectorID{
//			Miner:  abi.ActorID(mid),
//			Number: sectorID,
//		},
//		ProofType: si.SealProof,
//	}
//
//	log.Debugf("will call IsUnsealed now sector=%+v, offset=%d, size=%d", sectorID, offset, length)
//	return sa.pp.IsUnsealed(ctx, ref, storiface.UnpaddedByteIndex(offset), length)
//}

func (sa *sectorAccessor) sectorsStatus(ctx context.Context, sid abi.SectorNumber, showOnChainInfo bool) (api.SectorInfo, error) {
	sInfo, err := sa.secb.SectorsStatus(ctx, sid, false)
	if err != nil {
		return api.SectorInfo{}, err
	}

	if !showOnChainInfo {
		return sInfo, nil
	}

	onChainInfo, err := sa.full.StateSectorGetInfo(ctx, sa.maddr, sid, types.EmptyTSK)
	if err != nil {
		return sInfo, err
	}
	if onChainInfo == nil {
		return sInfo, nil
	}
	sInfo.SealProof = onChainInfo.SealProof
	sInfo.Activation = onChainInfo.Activation
	sInfo.Expiration = onChainInfo.Expiration
	sInfo.DealWeight = onChainInfo.DealWeight
	sInfo.VerifiedDealWeight = onChainInfo.VerifiedDealWeight
	sInfo.InitialPledge = onChainInfo.InitialPledge

	ex, err := sa.full.StateSectorExpiration(ctx, sa.maddr, sid, types.EmptyTSK)
	if err != nil {
		return sInfo, nil
	}
	sInfo.OnTime = ex.OnTime
	sInfo.Early = ex.Early

	return sInfo, nil
}
