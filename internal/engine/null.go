package engine

// Null is a no-op engine. It proves the spine runs with NO backend — the rebuild
// target and the test seam (swap it in and the owned layer must still function).
type Null struct{}

func NewNull() *Null { return &Null{} }

func (Null) Capabilities() Capabilities       { return Capabilities{Name: "null"} }
func (Null) Query(string, int) ([]Hit, error) { return nil, nil }
func (Null) Recall(string) ([]Fact, error)    { return nil, nil }
func (Null) Put(string, string) error         { return nil }
