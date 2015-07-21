// -*- mode: js2-mode -*-

// this device must be registered despite script load error
defineSomeDevice("nonFaultyDev");

noSuchFunction();

// this device isn't created or registered because script execution
// stops at noSuchFunction() call due to an error
defineSomeDevice("faultyDev");
