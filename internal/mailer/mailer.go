package mailer

import (
	"bytes"
	"embed"
	tt "text/template"
	ht "html/template"
	"time"

	"github.com/wneessen/go-mail"
)

//go:embed "templates"
var templatesFS embed.FS

type Mailer struct {
	client *mail.Client
	sender string
}

func NewMailer(host string , port int , username, password, sender string) (*Mailer, error) {
		client, err := mail.NewClient(
		host,
		mail.WithSMTPAuth(mail.SMTPAuthLogin),
		mail.WithUsername(username),
		mail.WithPassword(password),
		mail.WithTimeout(5*time.Second),
	)
	if err != nil {
		return nil, err
	}

	return &Mailer{
		client: client,
		sender: sender,
	}, nil
}

func (m *Mailer) Send(recipient, templateName string, data any) error {
	txt_tmpl , err := tt.New("").ParseFS(templatesFS , "templates/"+templateName)
	if err != nil {
		return err
	}

	subject := new(bytes.Buffer)

	err = txt_tmpl.ExecuteTemplate(subject , "subject" , data )
	if err != nil {
		return err
	}

	plainBody := new(bytes.Buffer)

	err = txt_tmpl.ExecuteTemplate(plainBody , "plainBody" , data )
	if err != nil {
		return err
	}

	// htmlParsing
	html_tmpl , err := ht.New("").ParseFS(templatesFS , "templates/"+templateName)
	if err != nil {
		return err
	}

	htmlBody := new(bytes.Buffer)

	err = html_tmpl.ExecuteTemplate(htmlBody , "htmlBody" , data )
	if err != nil {
		return err
	}

	msg := mail.NewMsg()
	err = msg.To(recipient)

	if err != nil {
		return err
	}

	err = msg.From(m.sender)
	if err != nil {
		return err
	}

	msg.Subject(subject.String())
	msg.SetBodyString(mail.TypeTextPlain, plainBody.String())
	msg.AddAlternativeString(mail.TypeTextHTML, htmlBody.String())

	for i := 0; i < 3; i++ {
		err = m.client.DialAndSend(msg)
		if err == nil {
			return nil
		}

		if i != 2{
			time.Sleep(800 * time.Millisecond)
		}
	}

	return err
}