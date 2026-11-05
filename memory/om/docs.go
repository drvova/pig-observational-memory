package om

// Doc is the signature and one-line doc of an exported om symbol, from the table generated from
// om's sources (docs_gen.go): name is a function or type, or Type.Method or Type.Field. The
// sandbox's describe(x) and the manual read it; false for a name the table does not hold.
func Doc(name string) (signature, synopsis string, ok bool) {
	d, ok := docs[name]
	return d[0], d[1], ok
}
