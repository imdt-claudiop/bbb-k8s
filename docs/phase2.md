# Proposta da Fase 2: células e controller

2026-10-21 · Tiago Daniel Jacobs · contra o repositório em 064e3e5

## Contexto

A fase 1 provou a unidade: o BigBlueButton 4.1 inteiro num cluster DOKS, um pod por serviço, reinstalável do zero (1m44s e zero restarts, segundo o relatório da fase 1). O estado de uma reunião vive na memória de singletons encadeados (akka-apps, bbb-web, LiveKit, SFU) e trafega por um Redis e um Postgres que servem aquele conjunto de reuniões. A unidade de escala é o stack inteiro.

A fase 2 replica a unidade. Cada réplica é uma **célula**: o chart da fase 1 instalado mais uma vez, dona das próprias reuniões, com Redis, Postgres e nó de mídia próprios. Dois akka-apps no mesmo banco se matariam a cada boot (o `preStart` do akka marca todas as reuniões do banco como encerradas), então células nunca compartilham akka, Redis ou Postgres.

O relatório da fase 1 propôs "células + router". Esta proposta muda duas coisas: o router sai, porque o balanceador que o integrador já usa faz esse papel; e entra um controller que cuida só do ciclo de vida da célula. Quantas células existem é decisão do operador; escala automática fica para depois.

## Decisões

| Decisão | O que significa | Por quê |
|---|---|---|
| Sem router próprio | O balanceador já em uso (Scalelite ou equivalente) escolhe a célula no `create`, redireciona o `join` e importa as gravações | O router era a peça com mais risco de virar produto; o balanceador já faz exatamente isso |
| Controller só de ciclo de vida | Sobe, registra, retira e remove células. Não decide quantas devem existir nem cria nós | Contagem é problema resolvido no Kubernetes (HPA, KEDA); nesta fase o operador escreve o número |
| Célula imutável | Nasce com uma versão e morre com ela. Upgrade é célula nova | Evita rollout em singletons que guardam estado em memória |
| Gravações provisórias | A célula publica, entrega ao integrador e pode apagar | É o modelo BBB + Scalelite: o servidor é descartável, o integrador guarda |
| Sem teto de capacidade | O balanceador espalha pela menor carga; o controller não mede ocupação | O teto só servia ao gatilho de escala, que saiu do escopo |
| Pod e nó dentro da célula viva | Fora do escopo | Fase 3 |

A regra da fase 1 continua valendo dentro da célula: só muda o que um serviço precisa para rodar fora de uma máquina única, e toda correção no BigBlueButton sai como PR upstream.

## A célula

Um release Helm do chart da fase 1 num namespace próprio (`cell-<n>`, nome nunca reutilizado), com os mesmos 18 pods de serviço, dois Jobs e uma CronJob, os mesmos quatro nós, um por papel (storage, media, control, data), e os mesmos quatro volumes. O nó de mídia é exclusivo: o livekit-server roda em hostNetwork e ocupa 7880, a porta TCP de mídia (443 em DOKS) e a faixa UDP 16384-32768. Quatro nós por célula é o que mantém o teto de usuários alto; compactar papéis num nó já é possível por values, mas baixa o teto e fica para quando os serviços em si escalarem.

**Valores próprios de cada célula**, gerados pelo controller: namespace, nome do LoadBalancer, host público (pelo template de nome do CellSet), `livekit.nodeIP` e `rtc.ips.includes` (IP público do nó de mídia e a faixa da VPC) e os Secrets de banco, salt, Hasura e LiveKit. Comuns a todas: o certificado TLS que cobre os hosts das células e a credencial de entrega de gravações. Não há pull secret: as imagens são as oficiais, públicas.

**Versão** da célula é o chart num commit: a tag única das imagens oficiais (`officialImages.tag`), o `schemaSha` e os arquivos do upstream sincronizados em `chart/files`, mais a cópia congelada do values.yaml. Trocar de versão é um commit do chart, nunca um `helm upgrade` da célula. O segundo `helm upgrade` do caminho `{{.ip}}` só troca o host, e as anotações de checksum do chart reiniciam os pods que o leem.

**Nós.** Um node pool por célula, criado pelo operador antes de escalar, com os rótulos `bbb-k8s/cell=<n>` e o papel; rótulo de pool sobrevive à troca de nó. Criar nó leva minutos e é o passo lento de uma célula nova.

**Estado fora do akka.** Os serviços auxiliares guardam pouco e por pouco tempo: shared-notes mantém os documentos no Postgres por 60 minutos depois da reunião, export-annotations usa uma fila no Redis e escreve no `bbb-data`, e o Redis guarda as filas do pipeline e os eventos de gravação por 14 dias. Webhooks registrados pela API vivem no Redis da célula: um integrador que usa `/hooks` registra em cada célula, e como o Scalelite propaga isso é ponto do PoC 3.

## A CRD e o controller

Uma CRD `CellSet` descreve um conjunto de células da mesma versão:

```yaml
apiVersion: bbb-k8s.imdt.dev/v1alpha1
kind: CellSet
metadata:
  name: bbb-41
spec:
  replicas: 2                       # escrito pelo operador
  provisionTimeout: 20m
  version:
    chart: 0.3.0
    valuesRef: bbb-41-values        # ConfigMap com a cópia congelada do values.yaml
  host:
    template: "cell-{{.index}}.bbb.exemplo.com"   # {{.ip}} disponível: nome derivado do IP, como na fase 1
    tlsSecretRef: bbb-wildcard-tls                # copiado para cada célula
  balancer:
    kind: scalelite
    url: https://scalelite.example/scalelite/api
    secretRef: scalelite-api
status:
  replicas: 2                       # células que servem ou vão servir
  selector: "bbb-k8s/cellset=bbb-41"
  cells:
    - {name: cell-1, phase: Registered}
    - {name: cell-2, phase: Provisioning}
```

A CRD declara o subresource `scale` (os três caminhos, inclusive `labelSelectorPath`, opcional na CRD mas obrigatório para o HPA). É o que faz `kubectl scale` funcionar agora e deixa um autoscaler entrar depois sem tocar no controller. Subiu `spec.replicas`, o controller cria células; desceu, retira a célula com menos participantes e, em empate, a mais antiga. `status.replicas` conta Provisioning, Ready e Registered, não Retiring, Retired ou Failed.

Ciclo de vida da célula, seis estados, sem volta depois de Retiring:

```
Provisioning ---API responde---> Ready ---register---> Registered
     |                             |                       |
     | prazo esgotado              | prazo esgotado        | upgrade ou redução
     v                             v                       v
   Failed <------------------------+                   Retiring
   (operador apaga)                                        |
                                                           | 0 reuniões e gravações entregues
                                                           v
                                                       Retired
                                                       (deregister, uninstall)
```

`Retiring` é o caminho sem volta: a célula sai da rotação para terminar em paz e só vira `Retired` com zero reuniões e as gravações entregues. `Failed` é Provisioning ou Ready que não chegou à API no prazo (`spec.provisionTimeout`, 20 minutos por padrão): fica no `status` para o operador apagar; o controller não substitui sozinho. Os nomes são os valores da fase na spec e não dependem do balanceador.

**Hooks.** Três, parte da spec, implementados por balanceador: `register(host, secret)`, `suspend` e `deregister`. O PoC implementa só o Scalelite, pela Servers API (desde a 1.5.1, autenticada por checksum como a API do BBB): `addServer` com a URL da API da célula e o segredo (nasce desabilitado, como na rake; a API não documenta o estado inicial) seguido de `updateServer state=enable`; `updateServer state=cordon`; `deleteServer`. Cordon é o único estado do Scalelite que bloqueia reuniões novas e deixa as existentes terminarem: `disable` não drena (pela API só muda o estado, e o `join` das reuniões existentes passa a falhar; pela rake, ou encerra todas ou não faz nada). O poller do Scalelite passa um servidor cordoned para disabled quando a carga zera. O segredo do Scalelite vive só no namespace do controller; nenhuma célula o recebe.

**Subir uma célula** (Provisioning, Ready, Registered):

1. Secrets da célula (inclusive a cópia do Secret TLS) e chart com o host do template, no node pool já criado. Se o template usa `{{.ip}}`, host provisório primeiro e um segundo `helm upgrade` com o nome final, como na fase 1.
2. Host resolvendo no DNS público para o IP do LB. Quem escreve o registro não é o controller: o external-dns pela anotação do Service, o resolvedor da IMDT ou o operador. Prazo: o mesmo `provisionTimeout`.
3. Todos os Deployments e StatefulSets `Ready` e o `helm test` passando (create, join, getMeetingInfo e end pelo host público). O DNS precisa resolver antes: o bbb-web baixa o `default.pdf` do host público no primeiro `create`.
4. `register`.

**Retirar uma célula** (Retiring, Retired): `suspend`; esperar zero reuniões; esperar as gravações entregues; `deregister`, só depois que o próprio balanceador reporta carga zero; `helm uninstall` (derruba o LB); esperar os volumes sumirem (classe Delete). O node pool fica para o operador. Sem teto de tempo: o `status` expõe há quanto tempo e o que falta (reuniões, gravações), e o operador decide encerrar pela API.

**Upgrade.** Um `CellSet` novo com a versão nova; quando ele tiver tantas células `Registered` quanto o antigo, o antigo recebe `replicas: 0` e todas as suas células passam por Retiring. O controller recusa reduzir a zero o único `CellSet` com células `Registered` num balanceador.

**Forma.** Operador com CRD desde o PoC, porque o subresource `scale` exige CRD. Roda num namespace próprio e não guarda estado fora da CRD. Cada transição idempotente e retomável.

## Escala

`kubectl scale cellset bbb-41 --replicas N`, pelo operador, com o node pool da célula nova já criado. Reunião não migra: reduzir é sempre retirar e esperar, uma célula por vez.

Criar célula leva minutos, então o pico das 08:00 com centenas de salas abrindo juntas se resolve antes dele: o operador escala com a antecedência medida no PoC 3 (tempo entre o `scale` e a célula `Registered`), com margem de duas vezes.

Escala automática fica para depois e não muda o controller: um ScaledObject do KEDA, ou qualquer autoscaler que fale com o subresource `scale`, escreve `spec.replicas`. Calendário para o pico previsível, métrica de ocupação para o imprevisto (o que exige o teto por célula, também fora desta fase), janela de estabilização longa na redução.

## Gravações

A entrega é por integrador; o PoC usa o caminho padrão do Scalelite: o `post_publish` `scalelite_post_publish.rb` empacota os formatos publicados num tar e faz `rsync` para o spool (`host:/path` por SSH); o importador do Scalelite (`recordings:watch`) importa e apaga o tar. Sinal de "entregue": o tar sumiu do spool e a gravação aparece no `getRecordings` do Scalelite. O playback passa a ser servido pelo Scalelite, então o link que o LMS recebe não depende do host da célula.

O que isso pede:

- **Egress.** Hoje o pod de recording só sai para o Redis e o DNS. Precisa de liberação para o host do spool e de uma credencial SSH comum às células, restrita ao diretório do spool e só para escrita.
- **Retenção.** O publicado fica no `bbb-data` (20 Gi) até a célula ser retirada; a CronJob diária de limpeza só apaga intermediários e chaves do Redis com mais de um dia. O `delete_recording` do script chama `bbb-record`, que não existe na imagem; apagar após a entrega é fase 3.
- **Condição de Retired.** Zero reuniões, fila de export-annotations vazia e nenhuma gravação da célula pendente de entrega, qualquer que seja o integrador. Gravação parada no pipeline aparece no `status` e se recupera pelo runbook (recriar o `.done` re-executa o archive preservando o `events.xml`, desde que a limpeza diária ainda não tenha apagado os intermediários).

## Pré-requisitos no chart

O chart de hoje não instala duas vezes no mesmo cluster. Estas mudanças vêm antes do controller.

| Bloqueio | Onde está | Mudança |
|---|---|---|
| Namespace fixo | `templates/namespace.yaml` falha fora de `bbb` | Remover a trava; uma célula por namespace |
| Nome fixo do LoadBalancer | Anotação `do-loadbalancer-name: bbb-k8s` | Derivar do namespace |
| Papéis de nó sem célula | `bbb-k8s/role=media` não diz de qual célula é o nó | Acrescentar `bbb-k8s/cell=<n>` ao nodeSelector, como rótulo de node pool |
| Políticas Cilium por entidade de nó | Qualquer nó alcança o Redis (sem senha) e o webhook do SFU de qualquer célula; qualquer pod alcança a 7880 de qualquer LiveKit | Nenhuma nesta fase: aceito e declarado. Sem Cilium (k3s) o chart já escreve políticas por IP do nó. Depois, restringir por rótulo de nó (`fromNodes`) e senha no Redis (#26110) |
| Secrets imperativos | `scripts/create-secrets.sh` gera uma vez, por namespace | O controller gera por célula e passa o segredo ao `register` |
| Volumes Retain | `do-block-storage-retain` em postgres, redis, bbb-data, bbb-recorder | Classe Delete nos quatro: os volumes morrem com a célula |
| Egress de gravação | Recording só sai para o Redis e o DNS | Regra para o host do spool |
| Firewall por droplet | UDP 16384-32768 liberado no firewall dos workers (docs/doks.md); nada automatiza isso para nó novo | Regra pela tag do node pool, aplicada pelo operador ao criar o pool |
| Nós de célula abertos a outras cargas | O nodeSelector prende os pods da célula aos nós dela, mas nada impede controller, Scalelite ou outra carga de cair neles | Taint `bbb-k8s/cell=<n>:NoSchedule` no node pool e toleration no chart |
| Auto-upgrade do DOKS | A DigitalOcean recicla nós em upgrade automático de patch e em surge upgrade: troca de nó em célula viva | Auto-upgrade desligado no cluster; a janela de manutenção é tratada como upgrade de célula |

## PoC

Marco único: duas células atrás de um Scalelite, o operador subindo a segunda com `kubectl scale` e o controller retirando a primeira num upgrade. Roda em DOKS, porque num k3s de uma máquina só cabe uma célula (hostNetwork do LiveKit). Em ordem, cada passo com reinstalação do zero e revisão antes do merge:

1. **Chart N vezes.** Critério: duas células no mesmo cluster, cada uma reinstalável do zero sem passo manual além do host e do node pool.
2. **Entrega de gravação ao Scalelite.** Critério: tar importado, spool vazio, playback pelo host do Scalelite.
3. **CRD e controller.** Critério: `kubectl scale` de 1 para 2 registra a segunda célula no Scalelite; de 2 para 1 retira a de menos participantes (em empate, a mais antiga) sem encerrar reunião; `Retired` só com zero reuniões e spool consumido. Mede o tempo do `scale` à célula `Registered`, que a DigitalOcean não documenta.
4. **Upgrade.** Critério: `CellSet` novo no ar, o antigo a zero, nenhuma reunião em curso encerrada pelo processo.

## Fora do escopo

Cortado desta fase para manter o PoC em quatro passos; volta quando houver motivo medido:

- Escala automática (KEDA ou HPA) e o que a alimentava: teto por célula, modos de ocupação, estado `Saturated`, hook `resume`, calibração do nó de mídia.
- Node pools criados pelo controller pela API da DigitalOcean; `Failed` substituída sozinha; teto de tempo em Retiring.
- Postgres e Redis em emptyDir: o bootstrap é um Job por TCP e não roda de novo quando o volume nasce vazio; os schemas e os metadados do Hasura já vêm das imagens oficiais. Com classe Delete, os volumes já morrem com a célula.
- Retenção de gravações pelo controller; `kind: webhook` para outros balanceadores (os nomes dos hooks já são agnósticos).
- Isolamento Cilium entre células.

Fase 3, por melhorias de arquitetura:

- Substituição de pods e manutenção de nós dentro de uma célula viva.
- Hidratação de estado do akka-apps e do bbb-web; réplicas de qualquer serviço; mídia multi-nó; TURN.
- Gravação desacoplada por fila e object storage; `bbb-data` fora da célula.
- SLOs em números.
- O que a fase 1 ainda deixa de fora: caption inbox, conversão de Office, telefonia, formatos de gravação além de presentation, notas dentro da gravação.