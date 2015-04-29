defineVirtualDevice("roCells", {
  title: "Readonly Cell Test",
  cells: {
    rocell: {
      type: "switch",
      value: false,
      readonly: true
    }
  }
});
