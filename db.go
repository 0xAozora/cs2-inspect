package inspect

type TokenDB interface {
	GetToken(string) (string, error)
	SetToken(string, string) error
}

type StubDB struct{}

func (s *StubDB) GetToken(name string) (string, error) {
	return "", nil
}
func (s *StubDB) SetToken(name, token string) error {
	return nil
}
