package tastora

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/celestiaorg/go-square/v4/share"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	"github.com/celestiaorg/tastora/framework/docker/dataavailability"
	"github.com/celestiaorg/tastora/framework/testutil/config"
	tomlutil "github.com/celestiaorg/tastora/framework/testutil/toml"
	"github.com/celestiaorg/tastora/framework/types"
	cometcfg "github.com/cometbft/cometbft/config"
	servercfg "github.com/cosmos/cosmos-sdk/server/config"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	nodeblob "github.com/celestiaorg/celestia-node/blob"
	"github.com/celestiaorg/celestia-node/state"
)

// restartHeightGap is how many heights the new chain leaves free above the
// highest old height, so no new header or vote shares a height with an old one.
const restartHeightGap = 10

// ExportRestartTestSuite checks DA nodes across a restart of the consensus
// network as a new chain from an export of its state.
type ExportRestartTestSuite struct {
	suite.Suite
	framework *Framework
}

func TestExportRestartTestSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping export restart tests in short mode")
	}
	suite.Run(t, &ExportRestartTestSuite{})
}

func (s *ExportRestartTestSuite) SetupSuite() {
	// bridge 0 follows the old chain, bridge 1 the new chain, and bridge 2
	// serves the old chain from an archive node after the restart.
	s.framework = NewFramework(s.T(), WithValidators(1), WithBridgeNodes(3), WithLightNodes(1))
	s.Require().NoError(s.framework.SetupNetwork(context.Background()))
}

// TestNewChainFromExport stops the chain, exports it and starts a new chain
// with a new chain-id at a height above every old one. A fresh bridge and
// light node join the new chain the way the DA recovery procedure says: a
// custom network with the hash of the new first block and sync from its
// height. The old chain stays readable through a bridge whose core is an
// archive node of the old chain.
func (s *ExportRestartTestSuite) TestNewChainFromExport() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	f := s.framework
	chain := f.GetCelestiaChain()
	archive := addArchiveNode(ctx, s.T(), chain)

	oldBridge := f.GetBridgeNodes()[0]
	namespace, err := share.NewV0Namespace(bytes.Repeat([]byte{0x07}, 10))
	s.Require().NoError(err)
	oldData := []byte("blob from the old chain")
	oldHeight, oldCommitment := s.submitBlob(ctx, oldBridge, namespace, oldData)
	s.T().Logf("old chain blob at height %d", oldHeight)

	newChainID := testChainID + "-restart"
	exportHeight, initialHeight := exportAndRestart(ctx, s.T(), chain, newChainID, archive)
	s.T().Logf("exported at height %d, %s starts at %d", exportHeight, newChainID, initialHeight)

	firstHash := blockHash(ctx, s.T(), chain.Validators[0], initialHeight)
	newBridge := s.startOnNewChain(ctx, f.daNetwork.GetBridgeNodes()[1], newChainID, firstHash, initialHeight, chain.Validators[0], "")
	f.bridgeNodes = append(f.bridgeNodes, newBridge)
	f.fundNodeAccount(ctx, newBridge, f.defaultFundingAmount)
	bridgeAddr := f.nodeP2PAddr(ctx, newBridge)
	light := s.startOnNewChain(ctx, f.daNetwork.GetLightNodes()[0], newChainID, firstHash, initialHeight, chain.Validators[0], bridgeAddr)

	s.Run("bridge syncs the new chain", func() {
		s.waitForHead(ctx, newBridge, uint64(initialHeight)+5)
	})

	newData := []byte("blob on the new chain")
	var newHeight uint64
	var newCommitment []byte
	s.Run("new blob submitted and read through the light node", func() {
		newHeight, newCommitment = s.submitBlob(ctx, newBridge, namespace, newData)
		s.Require().Greater(newHeight, uint64(initialHeight))
		s.waitForHead(ctx, light, newHeight)
		s.requireBlob(ctx, light, newHeight, namespace, newCommitment, newData)
	})

	s.Run("light node samples the new chain", func() {
		client := f.GetNodeRPCClient(ctx, light)
		err := waitUntil(ctx, 3*time.Minute, func() (bool, error) {
			stats, err := client.DAS.SamplingStats(ctx)
			if err != nil {
				return false, nil
			}
			return stats.SampledChainHead >= newHeight, nil
		})
		s.Require().NoError(err, "light node did not sample up to height %d", newHeight)
	})

	s.Run("old blob read from the archive", func() {
		archiveBridge := s.startArchiveBridge(ctx, f.daNetwork.GetBridgeNodes()[2], archive)
		s.waitForHead(ctx, archiveBridge, oldHeight)
		s.requireBlob(ctx, archiveBridge, oldHeight, namespace, oldCommitment, oldData)
	})
}

// submitBlob submits a blob through node and returns its height and commitment.
func (s *ExportRestartTestSuite) submitBlob(ctx context.Context, node *dataavailability.Node, namespace share.Namespace, data []byte) (uint64, []byte) {
	client := s.framework.GetNodeRPCClient(ctx, node)
	addr, err := client.State.AccountAddress(ctx)
	s.Require().NoError(err)
	libBlob, err := share.NewV1Blob(namespace, data, addr.Bytes())
	s.Require().NoError(err)
	blobs, err := nodeblob.ToNodeBlobs(libBlob)
	s.Require().NoError(err)
	height, err := client.Blob.Submit(ctx, blobs, state.NewTxConfig(state.WithGas(200_000), state.WithGasPrice(5000)))
	s.Require().NoError(err, "failed to submit a blob")
	_, err = client.Header.WaitForHeight(ctx, height)
	s.Require().NoError(err)
	return height, blobs[0].Commitment
}

// requireBlob checks that node returns the blob at height.
func (s *ExportRestartTestSuite) requireBlob(ctx context.Context, node *dataavailability.Node, height uint64, namespace share.Namespace, commitment, data []byte) {
	client := s.framework.GetNodeRPCClient(ctx, node)
	got, err := client.Blob.Get(ctx, height, namespace, commitment)
	s.Require().NoError(err, "failed to get the blob at height %d", height)
	s.Require().Equal(data, bytes.TrimRight(got.Data(), "\x00"))
}

// waitForHead waits until node has synced headers up to height.
func (s *ExportRestartTestSuite) waitForHead(ctx context.Context, node *dataavailability.Node, height uint64) {
	client := s.framework.GetNodeRPCClient(ctx, node)
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	_, err := client.Header.WaitForHeight(waitCtx, height)
	s.Require().NoError(err, "%s did not sync to height %d", node.Name(), height)
}

// startOnNewChain starts a DA node that has never run on the new chain: a
// custom network named after the new chain-id, with the hash of its first
// block, syncing from its first height. trustedPeer is empty for the bridge,
// which also bootstraps the new network.
func (s *ExportRestartTestSuite) startOnNewChain(ctx context.Context, node *dataavailability.Node, chainID, firstHash string, firstHeight int64, core *cosmos.ChainNode, trustedPeer string) *dataavailability.Node {
	info, err := core.GetNetworkInfo(ctx)
	s.Require().NoError(err)
	env := map[string]string{
		"CELESTIA_CUSTOM": types.BuildCelestiaCustomEnvVar(chainID, firstHash, trustedPeer),
		"P2P_NETWORK":     chainID,
	}
	if trustedPeer == "" {
		env["CELESTIA_BOOTSTRAPPER"] = "true"
	}
	syncFrom := map[string]tomlutil.Toml{
		"config.toml": {
			"RPC":    tomlutil.Toml{"SkipAuth": true},
			"Header": tomlutil.Toml{"Syncer": tomlutil.Toml{"SyncFromHeight": firstHeight}},
		},
	}
	err = node.Start(ctx,
		dataavailability.WithChainID(chainID),
		dataavailability.WithAdditionalStartArguments("--p2p.network", chainID, "--core.ip", info.Internal.Hostname, "--rpc.addr", "0.0.0.0"),
		dataavailability.WithEnvironmentVariables(env),
		dataavailability.WithConfigModifications(syncFrom),
	)
	s.Require().NoError(err, "failed to start %s on %s", node.Name(), chainID)
	return node
}

// startArchiveBridge starts a bridge on the old network whose core is the
// isolated archive node of the old chain.
func (s *ExportRestartTestSuite) startArchiveBridge(ctx context.Context, node *dataavailability.Node, archive *cosmos.ChainNode) *dataavailability.Node {
	f := s.framework
	info, err := archive.GetNetworkInfo(ctx)
	s.Require().NoError(err)
	err = node.Start(ctx,
		dataavailability.WithChainID(testChainID),
		dataavailability.WithAdditionalStartArguments("--p2p.network", testChainID, "--core.ip", info.Internal.Hostname, "--rpc.addr", "0.0.0.0"),
		dataavailability.WithEnvironmentVariables(map[string]string{
			"CELESTIA_CUSTOM": types.BuildCelestiaCustomEnvVar(testChainID, f.genesisHash, ""),
			"P2P_NETWORK":     testChainID,
		}),
	)
	s.Require().NoError(err, "failed to start the archive bridge")
	return node
}

// exportAndRestart stops the chain, exports the highest validator's state and
// restarts the validators as a new chain from it. The new chain starts at
// max(S, E) + 1 + restartHeightGap, where S is the highest height in any
// validator's sign state and E the export height, and the sign state is kept.
// archive keeps its data and is isolated from the new chain.
func exportAndRestart(ctx context.Context, t *testing.T, chain *cosmos.Chain, newChainID string, archive *cosmos.ChainNode) (exportHeight, initialHeight int64) {
	t.Helper()
	h, err := chain.Validators[0].Height(ctx)
	require.NoError(t, err)
	require.NoError(t, waitUntil(ctx, time.Minute, func() (bool, error) {
		ah, err := archive.Height(ctx)
		return err == nil && ah >= h, nil
	}), "archive did not reach height %d", h)
	require.NoError(t, chain.Stop(ctx), "failed to stop the chain")

	var exported *genutiltypes.AppGenesis
	var signed int64
	for _, v := range chain.Validators {
		cmd := []string{"celestia-appd", "export", "--home", v.HomeDir(), "--output-document", v.HomeDir() + "/export.json"}
		_, stderr, err := v.Exec(ctx, cmd, nil)
		require.NoError(t, err, "export failed on %s: %s", v.Name(), stderr)
		bz, err := v.ReadFile(ctx, "export.json")
		require.NoError(t, err)
		g, err := genutiltypes.AppGenesisFromReader(bytes.NewReader(bz))
		require.NoError(t, err)
		if exported == nil || g.InitialHeight > exported.InitialHeight {
			exported = g
		}
		signed = max(signed, signedHeight(ctx, t, v))
	}
	exportHeight = exported.InitialHeight - 1
	initialHeight = max(signed, exportHeight) + 1 + restartHeightGap

	exported.InitialHeight = initialHeight
	exported.ChainID = newChainID
	exported.GenesisTime = time.Now().Add(10 * time.Second).UTC()
	genesisBz, err := json.Marshal(exported)
	require.NoError(t, err)
	for _, v := range chain.Validators {
		require.NoError(t, v.WriteFile(ctx, "config/genesis.json", genesisBz))
		wipe := fmt.Sprintf("find %s/data -mindepth 1 -maxdepth 1 ! -name priv_validator_state.json ! -name tx_index.db -exec rm -rf {} +", v.HomeDir())
		_, stderr, err := v.Exec(ctx, []string{"sh", "-c", wipe}, nil)
		require.NoError(t, err, "failed to wipe data on %s: %s", v.Name(), stderr)
	}
	err = config.Modify(ctx, archive, "config/config.toml", func(cfg *cometcfg.Config) {
		cfg.P2P.PersistentPeers = ""
		cfg.P2P.Seeds = ""
		cfg.P2P.PexReactor = false
		cfg.P2P.MaxNumInboundPeers = 0
		cfg.P2P.MaxNumOutboundPeers = 0
	})
	require.NoError(t, err, "failed to isolate the archive")
	// Tastora signs with the chain-id of the node it broadcasts through.
	chain.Config.ChainID = newChainID
	for _, n := range chain.Validators {
		n.ChainID = newChainID
	}

	require.NoError(t, chain.Restart(ctx), "failed to restart the chain")
	require.NoError(t, waitUntil(ctx, 2*time.Minute, func() (bool, error) {
		nh, err := chain.Validators[0].Height(ctx)
		return err == nil && nh > initialHeight+2, nil
	}), "new chain did not produce blocks after height %d", initialHeight)
	return exportHeight, initialHeight
}

// signedHeight returns the height in a stopped validator's sign state.
func signedHeight(ctx context.Context, t *testing.T, node *cosmos.ChainNode) int64 {
	bz, err := node.ReadFile(ctx, "data/priv_validator_state.json")
	require.NoError(t, err)
	var st struct {
		Height string `json:"height"`
	}
	require.NoError(t, json.Unmarshal(bz, &st))
	h, err := strconv.ParseInt(st.Height, 10, 64)
	require.NoError(t, err)
	return h
}

// addArchiveNode adds a full node that keeps serving the old chain after the restart.
func addArchiveNode(ctx context.Context, t *testing.T, chain *cosmos.Chain) *cosmos.ChainNode {
	err := chain.AddNode(ctx, cosmos.NewChainNodeConfigBuilder().
		WithNodeType(types.NodeTypeConsensusFull).
		WithPostInit(func(ctx context.Context, node *cosmos.ChainNode) error {
			return config.Modify(ctx, node, "config/app.toml", func(cfg *servercfg.Config) {
				cfg.GRPC.Enable = true
			})
		}).
		Build())
	require.NoError(t, err, "failed to add the archive node")
	archive := chain.FullNodes[len(chain.FullNodes)-1]
	h, err := chain.Validators[0].Height(ctx)
	require.NoError(t, err)
	require.NoError(t, waitUntil(ctx, 2*time.Minute, func() (bool, error) {
		ah, err := archive.Height(ctx)
		return err == nil && ah >= h, nil
	}), "archive node did not sync")
	return archive
}

// blockHash returns the hash of the block at height on node.
func blockHash(ctx context.Context, t *testing.T, node *cosmos.ChainNode, height int64) string {
	c, err := node.GetRPCClient()
	require.NoError(t, err)
	block, err := c.Block(ctx, &height)
	require.NoError(t, err, "failed to get block %d", height)
	return block.Block.Header.Hash().String()
}

// waitUntil polls cond every second until it is true or timeout passes.
func waitUntil(ctx context.Context, timeout time.Duration, cond func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		ok, err := cond()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("condition not met within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
