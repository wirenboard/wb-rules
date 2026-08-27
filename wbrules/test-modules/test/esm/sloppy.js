// a legacy module in sloppy JavaScript: implicit globals the type checker
// would flag if its diagnostics were charged to an importing rule file
undeclaredGlobal = 1;
exports.f = function (a) { return a + undeclaredGlobal; };
