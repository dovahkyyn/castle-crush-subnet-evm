// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package precompile

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
)

var (
	_ StatefulPrecompileConfig = &ContractNativeMinterConfig{}
	// Singleton StatefulPrecompiledContract for minting native assets by permissioned callers.
	ContractNativeMinterPrecompile StatefulPrecompiledContract = createNativeMinterPrecompile(ContractNativeMinterAddress)

	mintSignature = CalculateFunctionSelector("mint(address,uint256)") // address, amount

	errCannotMint = errors.New("non-enabled cannot mint")

	mintInputLen = common.HashLength + common.HashLength
)

// ContractNativeMinterConfig wraps [AllowListConfig] and uses it to implement the StatefulPrecompileConfig
// interface while adding in the contract deployer specific precompile address.
type ContractNativeMinterConfig struct {
	AllowListConfig
}

// Address returns the address of the native minter contract.
func (c *ContractNativeMinterConfig) Address() common.Address {
	return ContractNativeMinterAddress
}

// Configure configures [state] with the desired admins based on [c].
func (c *ContractNativeMinterConfig) Configure(state StateDB) {
	c.AllowListConfig.Configure(state, ContractNativeMinterAddress)
}

// Contract returns the singleton stateful precompiled contract to be used for the native minter.
func (c *ContractNativeMinterConfig) Contract() StatefulPrecompiledContract {
	return ContractNativeMinterPrecompile
}

// GetContractNativeMinterStatus returns the role of [address] for the minter list.
func GetContractNativeMinterStatus(stateDB StateDB, address common.Address) AllowListRole {
	return getAllowListStatus(stateDB, ContractNativeMinterAddress, address)
}

// SetContractNativeMinterStatus sets the permissions of [address] to [role] for the
// minter list. assumes [role] has already been verified as valid.
func SetContractNativeMinterStatus(stateDB StateDB, address common.Address, role AllowListRole) {
	setAllowListRole(stateDB, ContractNativeMinterAddress, address, role)
}

// PackMintInput packs [address] and [amount] into the appropriate arguments for minting operation.
func PackMintInput(address common.Address, amount *big.Int) ([]byte, error) {
	// function selector (4 bytes) + input(hash for address + hash for amount)
	input := make([]byte, 0, selectorLen+mintInputLen)
	input = append(input, mintSignature...)
	input = append(input, address.Hash().Bytes()...)
	input = append(input, amount.Bytes()...)
	return input, nil
}

// UnpackMintInput attempts to unpack [input] into the arguments to the mint precompile
func UnpackMintInput(input []byte) (common.Address, *big.Int, error) {
	if len(input) != mintInputLen {
		return common.Address{}, nil, fmt.Errorf("invalid input length for minting: %d", len(input))
	}
	to := common.BytesToAddress(input[:common.AddressLength])
	assetAmount := new(big.Int).SetBytes(input[common.AddressLength : common.AddressLength+common.HashLength])
	return to, assetAmount, nil
}

// createMint checks if the caller is permissioned for minting operation.
// The execution function parses the [input] into native token amount and receiver address.
func createMint(accessibleState PrecompileAccessibleState, caller common.Address, addr common.Address, input []byte, suppliedGas uint64, readOnly bool) (ret []byte, remainingGas uint64, err error) {
	if suppliedGas < MintGasCost {
		return nil, 0, fmt.Errorf("%w (%d) < (%d)", vm.ErrOutOfGas, MintGasCost, suppliedGas)
	}
	remainingGas = suppliedGas - MintGasCost

	if readOnly {
		return nil, remainingGas, ErrWriteProtection
	}

	to, amount, err := UnpackMintInput(input)
	if err != nil {
		return nil, remainingGas, err
	}

	stateDB := accessibleState.GetStateDB()
	// Verify that the caller is in the allow list and therefore has the right to modify it
	callerStatus := getAllowListStatus(stateDB, ContractNativeMinterAddress, caller)
	if !callerStatus.IsEnabled() {
		return nil, remainingGas, fmt.Errorf("%w: %s", errCannotMint, caller)
	}

	if !stateDB.Exist(to) {
		if remainingGas < CallNewAccountGas {
			return nil, 0, fmt.Errorf("%w (%d) < (%d)", vm.ErrOutOfGas, CallNewAccountGas, suppliedGas)
		}
		remainingGas -= CallNewAccountGas
		stateDB.CreateAccount(to)
	}

	stateDB.AddBalance(to, amount)
	// Return an empty output and the remaining gas
	return []byte{}, remainingGas, nil
}

// createNativeMinterPrecompile returns a StatefulPrecompiledContract with R/W control of an allow list at [precompileAddr]
func createNativeMinterPrecompile(precompileAddr common.Address) StatefulPrecompiledContract {
	setAdmin := newStatefulPrecompileFunction(setAdminSignature, createAllowListRoleSetter(precompileAddr, AllowListAdmin))
	setEnabled := newStatefulPrecompileFunction(setEnabledSignature, createAllowListRoleSetter(precompileAddr, AllowListEnabled))
	setNone := newStatefulPrecompileFunction(setNoneSignature, createAllowListRoleSetter(precompileAddr, AllowListNoRole))
	read := newStatefulPrecompileFunction(readAllowListSignature, createReadAllowList(precompileAddr))

	mint := newStatefulPrecompileFunction(mintSignature, createMint)

	// Construct the contract with no fallback function.
	contract := newStatefulPrecompileWithFunctionSelectors(nil, []*statefulPrecompileFunction{setAdmin, setEnabled, setNone, read, mint})
	return contract
}