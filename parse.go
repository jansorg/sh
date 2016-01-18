// Copyright (c) 2016, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package sh

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
)

const (
	_ = -iota
	EOF
	IDENT
	STRING
)

func parse(r io.Reader, name string) error {
	p := &parser{
		r:    bufio.NewReader(r),
		name: name,
		line: 1,
		col:  0,
	}
	p.program()
	return p.err
}

type parser struct {
	r    *bufio.Reader
	tok  int32
	err  error
	name string
	line int
	col  int
}

var reserved = map[rune]bool{
	'\n': true,
	'#':  true,
	'=':  true,
	'&':  true,
	'>':  true,
	'<':  true,
	'|':  true,
	';':  true,
	'(':  true,
	')':  true,
	'{':  true,
	'}':  true,
}

var quote = map[rune]bool{
	'"':  true,
	'\'': true,
}

var space = map[rune]bool{
	' ':  true,
	'\t': true,
}

var ident = regexp.MustCompile(`^[a-zA-Z_]+[a-zA-Z0-9_]*$`)

func (p *parser) next() {
	r := ' '
	var err error
	for space[r] {
		r, _, err = p.r.ReadRune()
		if err == io.EOF {
			p.tok = EOF
			return
		}
		if err != nil {
			p.errPass(err)
			return
		}
		p.col++
	}
	if r == '\\' {
		p.next()
		if p.tok == '\n' {
			p.next()
			return
		}
		if err := p.r.UnreadRune(); err != nil {
			p.errPass(err)
			return
		}
		p.col--
	}
	if reserved[r] {
		if r == '#' {
			p.discardUpTo('\n')
			p.next()
			return
		}
		if r == '\n' {
			p.line++
			p.col = 0
		}
		p.tok = r
		return
	}
	if quote[r] {
		p.strContent(byte(r))
		if p.tok == EOF {
			return
		}
		p.tok = STRING
		return
	}
	runes := []rune{r}
	for !reserved[r] && !quote[r] && !space[r] {
		r, _, err = p.r.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			p.errPass(err)
			return
		}
		p.col++
		runes = append(runes, r)
	}
	if len(runes) > 1 {
		if err := p.r.UnreadRune(); err != nil {
			p.errPass(err)
			return
		}
		p.col--
		runes = runes[:len(runes)-1]
	}
	p.tok = STRING
	s := string(runes)
	if ident.MatchString(s) {
		p.tok = IDENT
	}
	return
}

func (p *parser) strContent(delim byte) {
	for {
		b, err := p.r.ReadBytes(delim)
		if err == io.EOF {
			p.errWanted(rune(delim))
		} else if err != nil {
			p.errPass(err)
		}
		if delim == '\'' {
			break
		}
		if len(b) > 1 && b[len(b)-2] == '\\' && b[len(b)-1] == delim {
			continue
		}
		break
	}
}

func (p *parser) discardUpTo(delim byte) {
	_, err := p.r.ReadBytes(delim)
	if err == io.EOF {
		p.tok = EOF
	} else if err != nil {
		p.errPass(err)
	}
}

func (p *parser) got(tok int32) bool {
	if p.tok == tok {
		p.next()
		return true
	}
	return false
}

func (p *parser) want(tok int32) {
	if p.tok != tok {
		p.errWanted(tok)
		return
	}
	p.next()
}

func tokStr(tok int32) string {
	switch tok {
	case EOF:
		return "EOF"
	case STRING:
		return "string"
	case IDENT:
		return "ident"
	default:
		return strconv.QuoteRune(tok)
	}
}

func (p *parser) errPass(err error) {
	p.err = err
	p.tok = EOF
}

func (p *parser) lineErr(format string, v ...interface{}) {
	pos := fmt.Sprintf("%s:%d:%d: ", p.name, p.line, p.col)
	p.errPass(fmt.Errorf(pos+format, v...))
}

func (p *parser) errUnexpected() {
	p.lineErr("unexpected token %s", tokStr(p.tok))
}

func (p *parser) errWanted(tok int32) {
	p.lineErr("unexpected token %s, wanted %s", tokStr(p.tok), tokStr(tok))
}

func (p *parser) program() {
	p.next()
	for p.tok != EOF {
		if p.got('\n') {
			continue
		}
		p.command()
	}
}

func (p *parser) command() {
	switch {
	case p.got(IDENT):
		for p.tok != EOF {
			switch {
			case p.got(IDENT):
			case p.got(STRING):
			case p.got('='):
				switch {
				case p.got(IDENT):
				case p.got(STRING):
				}
			case p.got('&'):
				if p.got('&') {
					p.command()
				}
				return
			case p.got('|'):
				p.got('|')
				p.command()
				return
			case p.got('('):
				p.want(')')
				p.want('{')
				for !p.got('}') {
					if p.tok == EOF {
						p.errWanted('}')
						break
					}
					if p.got('\n') {
						continue
					}
					p.command()
				}
				return
			case p.got('>'):
				switch {
				case p.got('>'):
				case p.got('&'):
				}
				p.value()
			case p.got('<'):
				p.value()
			case p.got(';'):
				return
			case p.got('\n'):
				return
			default:
				p.errUnexpected()
			}
		}
	case p.got(STRING):
		for p.tok != EOF {
			switch {
			case p.got(IDENT):
			case p.got(STRING):
			case p.got('&'):
				if p.got('&') {
					p.command()
				}
				return
			case p.got('|'):
				p.got('|')
				p.command()
				return
			case p.got('>'):
				switch {
				case p.got('>'):
				case p.got('&'):
				}
				p.value()
			case p.got('<'):
				p.value()
			case p.got(';'):
				return
			case p.got('\n'):
				return
			default:
				p.errUnexpected()
			}
		}
	case p.got('{'):
		for p.tok != EOF && p.tok != '}' {
			if p.got('\n') {
				continue
			}
			p.command()
		}
		p.want('}')
		switch {
		case p.got('&'):
			if p.got('&') {
				p.command()
			}
			return
		case p.got('|'):
			p.got('|')
			p.command()
			return
		case p.got('>'):
			switch {
			case p.got('>'):
			case p.got('&'):
			}
			p.value()
		case p.got('<'):
			p.value()
		case p.got(';'):
			return
		case p.got('\n'):
			return
		default:
			p.errUnexpected()
		}
	default:
		p.errUnexpected()
	}
}

func (p *parser) value() {
	switch {
	case p.got(IDENT):
	case p.got(STRING):
	default:
		p.errUnexpected()
	}
}
