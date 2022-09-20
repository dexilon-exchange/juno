package postgresql

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/forbole/juno/v3/logging"

	"github.com/cosmos/cosmos-sdk/simapp/params"
	"github.com/lib/pq"

	_ "github.com/lib/pq" // nolint

	"github.com/forbole/juno/v3/database"
	"github.com/forbole/juno/v3/types"
	"github.com/forbole/juno/v3/types/config"
)

// type check to ensure interface is properly implemented
var (
	_ database.Database              = &DatabaseTx{}
	_ database.SQLOperationIntegrity = &DatabaseTx{}
)

// DatabaseTx defines a wrapper around a SQL database and implements functionality
// for data aggregation and exporting.
type DatabaseTx struct {
	Sql            *sql.DB
	tx             *sql.Tx
	EncodingConfig *params.EncodingConfig
	Logger         logging.Logger
}

func (db *DatabaseTx) Begin() error {
	var err error
	db.tx, err = db.Sql.Begin()
	if err != nil {
		return err
	}
	return nil
}

func (db *DatabaseTx) Commit() error {
	db.conditionTxOpen()
	err := db.tx.Commit()
	db.tx = nil
	return err
}

func (db *DatabaseTx) Rollback() error {
	db.conditionTxOpen()
	err := db.tx.Rollback()
	db.tx = nil
	return err
}

func (db *DatabaseTx) conditionTxOpen() {
	if db.tx == nil {
		panic("db transaction is not started, call .Begin() to start")
	}
}

// createPartitionIfNotExists creates a new partition having the given partition id if not existing
func (db *DatabaseTx) createPartitionIfNotExists(table string, partitionID int64) error {
	db.conditionTxOpen()

	partitionTable := fmt.Sprintf("%s_%d", table, partitionID)

	stmt := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES IN (%d)",
		partitionTable,
		table,
		partitionID,
	)
	_, err := db.tx.Exec(stmt)

	if err != nil {
		return err
	}

	return nil
}

// -------------------------------------------------------------------------------------------------------------------

// HasBlock implements database.Database
func (db *DatabaseTx) HasBlock(height int64) (bool, error) {
	var res bool
	err := db.Sql.QueryRow(`SELECT EXISTS(SELECT 1 FROM block WHERE height = $1);`, height).Scan(&res)
	return res, err
}

// GetLastBlockHeight returns the last block height stored inside the database
func (db *DatabaseTx) GetLastBlockHeight() (int64, error) {
	stmt := `SELECT height FROM block ORDER BY height DESC LIMIT 1;`

	var height int64
	err := db.Sql.QueryRow(stmt).Scan(&height)
	if err != nil {
		if strings.Contains(err.Error(), "no rows in result set") {
			// If no rows stored in block table, return 0 as height
			return 0, nil
		}
		return 0, fmt.Errorf("error while getting last block height, error: %s", err)
	}

	return height, nil
}

// SaveBlock implements database.Database
func (db *DatabaseTx) SaveBlock(block *types.Block) error {
	db.conditionTxOpen()
	sqlStatement := `
INSERT INTO block (height, hash, num_txs, total_gas, proposer_address, timestamp)
VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`

	proposerAddress := sql.NullString{Valid: len(block.ProposerAddress) != 0, String: block.ProposerAddress}
	_, err := db.tx.Exec(sqlStatement,
		block.Height, block.Hash, block.TxNum, block.TotalGas, proposerAddress, block.Timestamp,
	)
	return err
}

// GetTotalBlocks implements database.Database
func (db *DatabaseTx) GetTotalBlocks() int64 {
	db.conditionTxOpen()
	var blockCount int64
	err := db.tx.QueryRow(`SELECT count(*) FROM block;`).Scan(&blockCount)
	if err != nil {
		return 0
	}

	return blockCount
}

// SaveTx implements database.Database
func (db *DatabaseTx) SaveTx(tx *types.Tx) error {
	db.conditionTxOpen()
	var partitionID int64

	partitionSize := config.Cfg.Database.PartitionSize
	if partitionSize > 0 {
		partitionID = tx.Height / partitionSize
		err := db.createPartitionIfNotExists("transaction", partitionID)
		if err != nil {
			return err
		}
	}

	return db.saveTxInsidePartition(tx, partitionID)
}

// saveTxInsidePartition stores the given transaction inside the partition having the given id
func (db *DatabaseTx) saveTxInsidePartition(tx *types.Tx, partitionId int64) error {
	db.conditionTxOpen()
	sqlStatement := `
INSERT INTO transaction 
(hash, height, success, messages, memo, signatures, signer_infos, fee, gas_wanted, gas_used, raw_log, logs, partition_id) 
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) 
ON CONFLICT (hash, partition_id) DO UPDATE 
	SET height = excluded.height, 
		success = excluded.success, 
		messages = excluded.messages,
		memo = excluded.memo, 
		signatures = excluded.signatures, 
		signer_infos = excluded.signer_infos,
		fee = excluded.fee, 
		gas_wanted = excluded.gas_wanted, 
		gas_used = excluded.gas_used,
		raw_log = excluded.raw_log, 
		logs = excluded.logs`

	var sigs = make([]string, len(tx.Signatures))
	for index, sig := range tx.Signatures {
		sigs[index] = base64.StdEncoding.EncodeToString(sig)
	}

	var msgs = make([]string, len(tx.Body.Messages))
	for index, msg := range tx.Body.Messages {
		bz, err := db.EncodingConfig.Marshaler.MarshalJSON(msg)
		if err != nil {
			return err
		}
		msgs[index] = string(bz)
	}
	msgsBz := fmt.Sprintf("[%s]", strings.Join(msgs, ","))

	feeBz, err := db.EncodingConfig.Marshaler.MarshalJSON(tx.AuthInfo.Fee)
	if err != nil {
		return fmt.Errorf("failed to JSON encode tx fee: %s", err)
	}

	var sigInfos = make([]string, len(tx.AuthInfo.SignerInfos))
	for index, info := range tx.AuthInfo.SignerInfos {
		bz, err := db.EncodingConfig.Marshaler.MarshalJSON(info)
		if err != nil {
			return err
		}
		sigInfos[index] = string(bz)
	}
	sigInfoBz := fmt.Sprintf("[%s]", strings.Join(sigInfos, ","))

	logsBz, err := db.EncodingConfig.Amino.MarshalJSON(tx.Logs)
	if err != nil {
		return err
	}

	_, err = db.tx.Exec(sqlStatement,
		tx.TxHash, tx.Height, tx.Successful(),
		msgsBz, tx.Body.Memo, pq.Array(sigs),
		sigInfoBz, string(feeBz),
		tx.GasWanted, tx.GasUsed, tx.RawLog, string(logsBz),
		partitionId,
	)
	return err
}

// HasValidator implements database.Database
func (db *DatabaseTx) HasValidator(addr string) (bool, error) {
	db.conditionTxOpen()
	var res bool
	stmt := `SELECT EXISTS(SELECT 1 FROM validator WHERE consensus_address = $1);`
	err := db.tx.QueryRow(stmt, addr).Scan(&res)
	return res, err
}

// SaveValidators implements database.Database
func (db *DatabaseTx) SaveValidators(validators []*types.Validator) error {
	db.conditionTxOpen()
	if len(validators) == 0 {
		return nil
	}

	stmt := `INSERT INTO validator (consensus_address, consensus_pubkey) VALUES `

	var vparams []interface{}
	for i, val := range validators {
		vi := i * 2

		stmt += fmt.Sprintf("($%d, $%d),", vi+1, vi+2)
		vparams = append(vparams, val.ConsAddr, val.ConsPubKey)
	}

	stmt = stmt[:len(stmt)-1] // Remove trailing ,
	stmt += " ON CONFLICT DO NOTHING"
	_, err := db.tx.Exec(stmt, vparams...)
	return err
}

// SaveCommitSignatures implements database.Database
func (db *DatabaseTx) SaveCommitSignatures(signatures []*types.CommitSig) error {
	db.conditionTxOpen()
	if len(signatures) == 0 {
		return nil
	}

	stmt := `INSERT INTO pre_commit (validator_address, height, timestamp, voting_power, proposer_priority) VALUES `

	var sparams []interface{}
	for i, sig := range signatures {
		si := i * 5

		stmt += fmt.Sprintf("($%d, $%d, $%d, $%d, $%d),", si+1, si+2, si+3, si+4, si+5)
		sparams = append(sparams, sig.ValidatorAddress, sig.Height, sig.Timestamp, sig.VotingPower, sig.ProposerPriority)
	}

	stmt = stmt[:len(stmt)-1]
	stmt += " ON CONFLICT (validator_address, timestamp) DO NOTHING"
	_, err := db.tx.Exec(stmt, sparams...)
	return err
}

// SaveMessage implements database.Database
func (db *DatabaseTx) SaveMessage(msg *types.Message) error {
	db.conditionTxOpen()
	var partitionID int64
	partitionSize := config.Cfg.Database.PartitionSize
	if partitionSize > 0 {
		partitionID = msg.Height / partitionSize
		err := db.createPartitionIfNotExists("message", partitionID)
		if err != nil {
			return err
		}
	}

	return db.saveMessageInsidePartition(msg, partitionID)
}

// saveMessageInsidePartition stores the given message inside the partition having the provided id
func (db *DatabaseTx) saveMessageInsidePartition(msg *types.Message, partitionID int64) error {
	db.conditionTxOpen()
	stmt := `
INSERT INTO message(transaction_hash, index, type, value, involved_accounts_addresses, height, partition_id) 
VALUES ($1, $2, $3, $4, $5, $6, $7) 
ON CONFLICT (transaction_hash, index, partition_id) DO UPDATE 
	SET height = excluded.height, 
		type = excluded.type,
		value = excluded.value,
		involved_accounts_addresses = excluded.involved_accounts_addresses`

	_, err := db.tx.Exec(stmt, msg.TxHash, msg.Index, msg.Type, msg.Value, pq.Array(msg.Addresses), msg.Height, partitionID)
	return err
}

// Close implements database.Database
func (db *DatabaseTx) Close() {
	if db.tx != nil {
		err := db.Rollback()
		if err != nil {
			db.Logger.Error("error while closing connection tx rollback", "err", err)
		}
	}
	err := db.Sql.Close()
	if err != nil {
		db.Logger.Error("error while closing connection", "err", err)
	}
}

// -------------------------------------------------------------------------------------------------------------------

// GetLastPruned implements database.PruningDb
func (db *DatabaseTx) GetLastPruned() (int64, error) {
	db.conditionTxOpen()
	var lastPrunedHeight int64
	err := db.tx.QueryRow(`SELECT coalesce(MAX(last_pruned_height),0) FROM pruning LIMIT 1;`).Scan(&lastPrunedHeight)
	return lastPrunedHeight, err
}

// StoreLastPruned implements database.PruningDb
func (db *DatabaseTx) StoreLastPruned(height int64) error {
	db.conditionTxOpen()
	_, err := db.tx.Exec(`DELETE FROM pruning`)
	if err != nil {
		return err
	}

	_, err = db.tx.Exec(`INSERT INTO pruning (last_pruned_height) VALUES ($1)`, height)
	return err
}

// Prune implements database.PruningDb
func (db *DatabaseTx) Prune(height int64) error {
	db.conditionTxOpen()
	_, err := db.tx.Exec(`DELETE FROM pre_commit WHERE height = $1`, height)
	if err != nil {
		return err
	}

	_, err = db.tx.Exec(`
DELETE FROM message 
USING transaction 
WHERE message.transaction_hash = transaction.hash AND transaction.height = $1
`, height)
	return err
}
