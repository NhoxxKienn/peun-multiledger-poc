// Chain A for the COORDINATED multi-ledger virtual-channel PoC.
//
// 500 ms auto-mine interval so the dispute/coordinate path completes quickly.
// Five pre-funded accounts: deployer, Alice, Bob, Ingrid (intermediary), and
// Charlie (the trusted cross-chain coordinator). See ../README.md for topology.
export default {
  solidity: "0.8.28",
  networks: {
    hardhat: {
      type: "edr-simulated",
      chainId: 1337,
      accounts: [
        {
          privateKey: "0x79ea8f62d97bc0591a4224c1725fca6b00de5b2cea286fe2e0bb35c5e76be46e",
          balance: "10000000000000000000000"
        },
        {
          privateKey: "0x1af2e950272dd403de7a5760d41c6e44d92b6d02797e51810795ff03cc2cda4f",
          balance: "10000000000000000000000"
        },
        {
          privateKey: "0xf63d7d8e930bccd74e93cf5662fde2c28fd8be95edb70c73f1bdd863d07f412e",
          balance: "10000000000000000000000"
        },
        {
          privateKey: "0x9c7d3e8f1a2b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f60718293",
          balance: "10000000000000000000000"
        },
        {
          privateKey: "0x1c8e5b9a7c3d2e4f8a1b2c3d4e5f67890abcdef1234567890abcdef123456789",
          balance: "10000000000000000000000"
        }
      ],
      blockGasLimit: 12000000,
      mining: {
        auto: true,
        interval: 500
      }
    }
  }
};
