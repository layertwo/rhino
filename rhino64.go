package main

import (
    "github.com/go-redis/redis"
    "github.com/miekg/dns"
    "log"
    "net"
    "strconv"
    "time"
)

var (
    prefix = net.IP{0, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
    client = redis.NewClient(&redis.Options{
        Addr: "localhost:32768",
        Password: "",
        DB: 0,
    })
)

func main() {
    dns.HandleFunc(".", handleRequest)
    err := dns.ListenAndServe(":53", "udp6", nil)
    if err != nil {
        log.Fatal("ListenAndServe: ", err)
    }
}

func makeRequest(name string, qtype uint16, recursion bool) *dns.Msg {

    log.Printf("querying %s record for %s\n", dns.TypeToString[qtype], name)
    cache_name := strconv.Itoa(int(qtype)) + "-" + name

    // check cache
    val, err := client.Get(cache_name).Result()
    if err == redis.Nil {

        log.Printf("%s not found in cache", name)

        c := new(dns.Client)
        m := new(dns.Msg)
        m.SetQuestion(dns.Fqdn(name), qtype)
        m.RecursionDesired = recursion

        r, _, err := c.Exchange(m, net.JoinHostPort("8.8.8.8", "53"))
        if err != nil {
            log.Fatalf("error: %s\n", err)
        }
        go func() {
            msg, err := r.Pack()
            err = client.Set(cache_name, msg, 60*time.Second).Err()
            if err != nil {
                log.Fatalf("error setting cache for %s with error %s", name, err)
            } else {
                log.Printf("added %s to cache", name)
            }
        }()
        return r

    } else {
        log.Printf("found %s in cache", name)
        m := new(dns.Msg)
        m.Unpack([]byte(val))
        return m
    }
}

func handleRequest(w dns.ResponseWriter, req *dns.Msg) {

    m := new(dns.Msg)
    m.SetReply(req)

    for _, q := range m.Question {

        r := new(dns.Msg)

        r = makeRequest(q.Name, q.Qtype, req.MsgHdr.RecursionDesired)
        if r.Rcode != dns.RcodeSuccess {
            continue
        }

        if len(r.Answer) > 0 {
            log.Printf("found %v answer(s) for %s", len(r.Answer), q.Name)
            for _, a := range r.Answer{
                m.Answer = append(m.Answer, a)
            }

        } else if q.Qtype != dns.TypeSOA {
            log.Printf("did not find %s answer for %s", dns.TypeToString[q.Qtype], q.Name)

            switch q.Qtype {
            case dns.TypeAAAA:
                log.Printf("generating synthetic IPv6 addr for %s", q.Name)

                r = makeRequest(q.Name, dns.TypeA, req.MsgHdr.RecursionDesired)
                if len(r.Answer) > 0 {
                    log.Printf("found %v answers for %s", len(r.Answer), q.Name)

                    for _, a := range r.Answer{
                        record := a.(*dns.A)
                        rr := &dns.AAAA{
                            Hdr:  dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: record.Hdr.Class, Ttl: record.Hdr.Ttl},
                            AAAA: makeSyntheticIPv6(record.A),
                        }
                        m.Answer = append(m.Answer, rr)
                    }
                }
        }
    }

    // carry soa name servers forward
    m.Ns = r.Ns
    id := m.MsgHdr.Id
    m.MsgHdr = r.MsgHdr
    m.MsgHdr.Id = id
    }

    w.WriteMsg(m)

}

func makeSyntheticIPv6(ip net.IP) net.IP {

    synth := prefix
    synth[12] = ip[0]
    synth[13] = ip[1]
    synth[14] = ip[2]
    synth[15] = ip[3]

    return synth

}
