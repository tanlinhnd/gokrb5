package messages

// Reference: https://www.ietf.org/rfc/rfc4120.txt
// Section: 5.4.2

import (
	"errors"
	"fmt"
	"github.com/jcmturner/asn1"
	"github.com/jcmturner/gokrb5/config"
	"github.com/jcmturner/gokrb5/credentials"
	"github.com/jcmturner/gokrb5/crypto"
	"github.com/jcmturner/gokrb5/crypto/engine"
	"github.com/jcmturner/gokrb5/iana/asnAppTag"
	"github.com/jcmturner/gokrb5/iana/keyusage"
	"github.com/jcmturner/gokrb5/iana/msgtype"
	"github.com/jcmturner/gokrb5/iana/patype"
	"github.com/jcmturner/gokrb5/types"
	"time"
)

type marshalKDCRep struct {
	PVNO    int                  `asn1:"explicit,tag:0"`
	MsgType int                  `asn1:"explicit,tag:1"`
	PAData  types.PADataSequence `asn1:"explicit,optional,tag:2"`
	CRealm  string               `asn1:"generalstring,explicit,tag:3"`
	CName   types.PrincipalName  `asn1:"explicit,tag:4"`
	// Ticket needs to be a raw value as it is wrapped in an APPLICATION tag
	Ticket  asn1.RawValue       `asn1:"explicit,tag:5"`
	EncPart types.EncryptedData `asn1:"explicit,tag:6"`
}

// KRB_KDC_REP struct fields.
type KDCRepFields struct {
	PVNO             int
	MsgType          int
	PAData           []types.PAData
	CRealm           string
	CName            types.PrincipalName
	Ticket           Ticket
	EncPart          types.EncryptedData
	DecryptedEncPart EncKDCRepPart
}

// RFC 4120 KRB_AS_REP: https://tools.ietf.org/html/rfc4120#section-5.4.2.
type ASRep struct {
	KDCRepFields
}

// RFC 4120 KRB_TGS_REP: https://tools.ietf.org/html/rfc4120#section-5.4.2.
type TGSRep struct {
	KDCRepFields
}

// Encrypted part of KRB_KDC_REP.
type EncKDCRepPart struct {
	Key           types.EncryptionKey  `asn1:"explicit,tag:0"`
	LastReqs      []LastReq            `asn1:"explicit,tag:1"`
	Nonce         int                  `asn1:"explicit,tag:2"`
	KeyExpiration time.Time            `asn1:"generalized,explicit,optional,tag:3"`
	Flags         asn1.BitString       `asn1:"explicit,tag:4"`
	AuthTime      time.Time            `asn1:"generalized,explicit,tag:5"`
	StartTime     time.Time            `asn1:"generalized,explicit,optional,tag:6"`
	EndTime       time.Time            `asn1:"generalized,explicit,tag:7"`
	RenewTill     time.Time            `asn1:"generalized,explicit,optional,tag:8"`
	SRealm        string               `asn1:"generalstring,explicit,tag:9"`
	SName         types.PrincipalName  `asn1:"explicit,tag:10"`
	CAddr         []types.HostAddress  `asn1:"explicit,optional,tag:11"`
	EncPAData     types.PADataSequence `asn1:"explicit,optional,tag:12"`
}

// LastReq part of KRB_KDC_REP.
type LastReq struct {
	LRType  int       `asn1:"explicit,tag:0"`
	LRValue time.Time `asn1:"generalized,explicit,tag:1"`
}

// Unmarshal bytes b into the ASRep struct.
func (k *ASRep) Unmarshal(b []byte) error {
	var m marshalKDCRep
	_, err := asn1.UnmarshalWithParams(b, &m, fmt.Sprintf("application,explicit,tag:%v", asnAppTag.ASREP))
	if err != nil {
		return processReplyError(b, err)
	}
	if m.MsgType != msgtype.KRB_AS_REP {
		return errors.New("Message ID does not indicate a KRB_AS_REP")
	}
	//Process the raw ticket within
	tkt, err := UnmarshalTicket(m.Ticket.Bytes)
	if err != nil {
		return err
	}
	k.KDCRepFields = KDCRepFields{
		PVNO:    m.PVNO,
		MsgType: m.MsgType,
		PAData:  m.PAData,
		CRealm:  m.CRealm,
		CName:   m.CName,
		Ticket:  tkt,
		EncPart: m.EncPart,
	}
	return nil
}

// Unmarshal bytes b into the TGSRep struct.
func (k *TGSRep) Unmarshal(b []byte) error {
	var m marshalKDCRep
	_, err := asn1.UnmarshalWithParams(b, &m, fmt.Sprintf("application,explicit,tag:%v", asnAppTag.TGSREP))
	if err != nil {
		return processReplyError(b, err)
	}
	if m.MsgType != msgtype.KRB_TGS_REP {
		return errors.New("Message ID does not indicate a KRB_TGS_REP")
	}
	//Process the raw ticket within
	tkt, err := UnmarshalTicket(m.Ticket.Bytes)
	if err != nil {
		return err
	}
	k.KDCRepFields = KDCRepFields{
		PVNO:    m.PVNO,
		MsgType: m.MsgType,
		PAData:  m.PAData,
		CRealm:  m.CRealm,
		CName:   m.CName,
		Ticket:  tkt,
		EncPart: m.EncPart,
	}
	return nil
}

// Unmarshal bytes b into encrypted part of KRB_KDC_REP.
func (e *EncKDCRepPart) Unmarshal(b []byte) error {
	_, err := asn1.UnmarshalWithParams(b, e, fmt.Sprintf("application,explicit,tag:%v", asnAppTag.EncASRepPart))
	if err != nil {
		// Try using tag 26
		/* Ref: RFC 4120
		Compatibility note: Some implementations unconditionally send an
		encrypted EncTGSRepPart (application tag number 26) in this field
		regardless of whether the reply is a AS-REP or a TGS-REP.  In the
		interest of compatibility, implementors MAY relax the check on the
		tag number of the decrypted ENC-PART.*/
		_, err = asn1.UnmarshalWithParams(b, e, fmt.Sprintf("application,explicit,tag:%v", asnAppTag.EncTGSRepPart))
		return err
	}
	return err
}

// Decrypt the encrypted part of an AS_REP.
func (k *ASRep) DecryptEncPart(c *credentials.Credentials) (types.EncryptionKey, error) {
	var key types.EncryptionKey
	var err error
	if c.HasKeytab() {
		key, err = c.Keytab.GetEncryptionKey(k.CName.NameString, k.CRealm, k.EncPart.KVNO, k.EncPart.EType)
		if err != nil {
			return key, fmt.Errorf("Could not get key from keytab: %v", err)
		}
	}
	if c.HasPassword() {
		key, _, err = crypto.GetKeyFromPassword(c.Password, k.CName, k.CRealm, k.EncPart.EType, k.PAData)
		if err != nil {
			return key, fmt.Errorf("Could not derive key from password: %v", err)
		}
	}
	if !c.HasKeytab() && !c.HasPassword() {
		return key, errors.New("No secret available in credentials to preform decryption")
	}
	b, err := crypto.DecryptEncPart(k.EncPart, key, keyusage.AS_REP_ENCPART)
	if err != nil {
		return key, fmt.Errorf("Error decrypting KDC_REP EncPart: %v", err)
	}
	var denc EncKDCRepPart
	err = denc.Unmarshal(b)
	if err != nil {
		return key, fmt.Errorf("Error unmarshalling encrypted part: %v", err)
	}
	k.DecryptedEncPart = denc
	return key, nil
}

// Check validity of AS_REP message.
func (k *ASRep) IsValid(cfg *config.Config, creds *credentials.Credentials, asReq ASReq) (bool, error) {
	//Ref RFC 4120 Section 3.1.5
	if k.CName.NameType != asReq.ReqBody.CName.NameType || k.CName.NameString == nil {
		return false, fmt.Errorf("CName in response does not match what was requested. Requested: %+v; Reply: %+v", asReq.ReqBody.CName, k.CName)
	}
	for i := range k.CName.NameString {
		if k.CName.NameString[i] != asReq.ReqBody.CName.NameString[i] {
			return false, fmt.Errorf("CName in response does not match what was requested. Requested: %+v; Reply: %+v", asReq.ReqBody.CName, k.CName)
		}
	}
	if k.CRealm != asReq.ReqBody.Realm {
		return false, fmt.Errorf("CRealm in response does not match what was requested. Requested: %s; Reply: %s", asReq.ReqBody.Realm, k.CRealm)
	}
	key, err := k.DecryptEncPart(creds)
	if err != nil {
		return false, fmt.Errorf("Error decrypting EncPart of AS_REP: %v", err)
	}
	if k.DecryptedEncPart.Nonce != asReq.ReqBody.Nonce {
		return false, errors.New("Possible replay attack, nonce in response does not match that in request")
	}
	if k.DecryptedEncPart.SName.NameType != asReq.ReqBody.SName.NameType || k.DecryptedEncPart.SName.NameString == nil {
		return false, fmt.Errorf("SName in response does not match what was requested. Requested: %v; Reply: %v", asReq.ReqBody.SName, k.DecryptedEncPart.SName)
	}
	for i := range k.CName.NameString {
		if k.DecryptedEncPart.SName.NameString[i] != asReq.ReqBody.SName.NameString[i] {
			return false, fmt.Errorf("SName in response does not match what was requested. Requested: %+v; Reply: %+v", asReq.ReqBody.SName, k.DecryptedEncPart.SName)
		}
	}
	if k.DecryptedEncPart.SRealm != asReq.ReqBody.Realm {
		return false, fmt.Errorf("SRealm in response does not match what was requested. Requested: %s; Reply: %s", asReq.ReqBody.Realm, k.DecryptedEncPart.SRealm)
	}
	if len(asReq.ReqBody.Addresses) > 0 {
		if !types.HostAddressesEqual(k.DecryptedEncPart.CAddr, asReq.ReqBody.Addresses) {
			return false, errors.New("Addresses listed in the AS_REP does not match those listed in the AS_REQ")
		}
	}
	t := time.Now().UTC()
	if t.Sub(k.DecryptedEncPart.AuthTime) > cfg.LibDefaults.Clockskew || k.DecryptedEncPart.AuthTime.Sub(t) > cfg.LibDefaults.Clockskew {
		return false, fmt.Errorf("Clock skew with KDC too large. Greater than %v seconds", cfg.LibDefaults.Clockskew.Seconds())
	}
	// RFC 6806 https://tools.ietf.org/html/rfc6806.html#section-11
	if asReq.PAData.Contains(patype.PA_REQ_ENC_PA_REP) && types.IsFlagSet(&k.DecryptedEncPart.Flags, types.EncPARep) {
		if len(k.DecryptedEncPart.EncPAData) < 2 || !k.DecryptedEncPart.EncPAData.Contains(patype.PA_FX_FAST) {
			return false, errors.New("KDC did not respond appropriately to FAST negotiation")
		}
		for _, pa := range k.DecryptedEncPart.EncPAData {
			if pa.PADataType == patype.PA_REQ_ENC_PA_REP {
				var pafast types.PAReqEncPARep
				err := pafast.Unmarshal(pa.PADataValue)
				if err != nil {
					return false, fmt.Errorf("KDC FAST negotiation response error, could not unmarshal PA_REQ_ENC_PA_REP: %v", err)
				}
				etype, err := crypto.GetChksumEtype(pafast.ChksumType)
				if err != nil {
					return false, fmt.Errorf("KDC FAST negotiation response error, %v", err)
				}
				ab, _ := asReq.Marshal()
				if !engine.VerifyChecksum(key.KeyValue, pafast.Chksum, ab, keyusage.KEY_USAGE_AS_REQ, etype) {
					return false, errors.New("KDC FAST negotiation response checksum invalid")
				}
			}
		}
	}
	return true, nil
}

// Decrypt the encrypted part of an TGS_REP.
func (k *TGSRep) DecryptEncPart(key types.EncryptionKey) error {
	b, err := crypto.DecryptEncPart(k.EncPart, key, keyusage.TGS_REP_ENCPART_SESSION_KEY)
	if err != nil {
		return fmt.Errorf("Error decrypting KDC_REP EncPart: %v", err)
	}
	var denc EncKDCRepPart
	err = denc.Unmarshal(b)
	if err != nil {
		return fmt.Errorf("Error unmarshalling encrypted part: %v", err)
	}
	k.DecryptedEncPart = denc
	return nil
}

// Check validity of TGS_REP message.
func (k *TGSRep) IsValid(cfg *config.Config, tgsReq TGSReq) (bool, error) {
	if k.CName.NameType != tgsReq.ReqBody.CName.NameType || k.CName.NameString == nil {
		return false, fmt.Errorf("CName in response does not match what was requested. Requested: %+v; Reply: %+v", tgsReq.ReqBody.CName, k.CName)
	}
	for i := range k.CName.NameString {
		if k.CName.NameString[i] != tgsReq.ReqBody.CName.NameString[i] {
			return false, fmt.Errorf("CName in response does not match what was requested. Requested: %+v; Reply: %+v", tgsReq.ReqBody.CName, k.CName)
		}
	}
	if k.CRealm != tgsReq.ReqBody.Realm {
		return false, fmt.Errorf("CRealm in response does not match what was requested. Requested: %s; Reply: %s", tgsReq.ReqBody.Realm, k.CRealm)
	}
	if k.DecryptedEncPart.Nonce != tgsReq.ReqBody.Nonce {
		return false, errors.New("Possible replay attack, nonce in response does not match that in request")
	}
	if k.Ticket.SName.NameType != tgsReq.ReqBody.SName.NameType || k.Ticket.SName.NameString == nil {
		return false, fmt.Errorf("SName in response ticket does not match what was requested. Requested: %v; Reply: %v", tgsReq.ReqBody.SName, k.Ticket.SName)
	}
	for i := range k.Ticket.SName.NameString {
		if k.Ticket.SName.NameString[i] != tgsReq.ReqBody.SName.NameString[i] {
			return false, fmt.Errorf("SName in response ticket does not match what was requested. Requested: %+v; Reply: %+v", tgsReq.ReqBody.SName, k.Ticket.SName)
		}
	}
	if k.DecryptedEncPart.SName.NameType != tgsReq.ReqBody.SName.NameType || k.DecryptedEncPart.SName.NameString == nil {
		return false, fmt.Errorf("SName in response does not match what was requested. Requested: %v; Reply: %v", tgsReq.ReqBody.SName, k.DecryptedEncPart.SName)
	}
	for i := range k.CName.NameString {
		if k.DecryptedEncPart.SName.NameString[i] != tgsReq.ReqBody.SName.NameString[i] {
			return false, fmt.Errorf("SName in response does not match what was requested. Requested: %+v; Reply: %+v", tgsReq.ReqBody.SName, k.DecryptedEncPart.SName)
		}
	}
	if k.DecryptedEncPart.SRealm != tgsReq.ReqBody.Realm {
		return false, fmt.Errorf("SRealm in response does not match what was requested. Requested: %s; Reply: %s", tgsReq.ReqBody.Realm, k.DecryptedEncPart.SRealm)
	}
	if len(tgsReq.ReqBody.Addresses) > 0 {
		if !types.HostAddressesEqual(k.DecryptedEncPart.CAddr, tgsReq.ReqBody.Addresses) {
			return false, errors.New("Addresses listed in the TGS_REP does not match those listed in the TGS_REQ")
		}
	}
	if time.Since(k.DecryptedEncPart.StartTime) > cfg.LibDefaults.Clockskew || k.DecryptedEncPart.StartTime.Sub(time.Now().UTC()) > cfg.LibDefaults.Clockskew {
		if time.Since(k.DecryptedEncPart.AuthTime) > cfg.LibDefaults.Clockskew || k.DecryptedEncPart.AuthTime.Sub(time.Now().UTC()) > cfg.LibDefaults.Clockskew {
			return false, fmt.Errorf("Clock skew with KDC too large. Greater than %v seconds.", cfg.LibDefaults.Clockskew.Seconds())
		}
	}
	return true, nil
}
