# PODTetris thesis

## Chapters

### Introduction

- PoliTO
- Logistics Reply

### Prerequisites

- Kubernetes
- Docker
- Cloud Providers:
    Amazon EKS
    includi che non possiamo modificare lo scheduler! dovremmo farne girare un'altra istanza

### The problem of resource fragmentation

Existing tools:

- Cluster Autoscaler
    threshold fisso
    no shuflling!
    nessuna decisione sulla destinazioni dei pod di rimpiazzo: verifica solo se ci entrano su altri nodi
- Descheduler
  - pros
  - cons
    nessuna garanzia spostamento
        eviction hoping pods wont end up on the same nodes, causing useless disruption
- AWS Karpenter
    threshold fisso
    consolidamento taglie

### Related works

- OR Tools (2 papers)
- tesi Bologna Kubernetes
- Paper Canova
- descheduler impact on resource fragmentation
- fondazione kessler

### Kubernetes

#### Vincoli

- affinity  / anti-affinity
- nodeSelector
- topology areas
- pod disruption budgets

#### Scheduler

- Filter + Scoring phases
  - LeastAllocated + MostAllocated
    no control planes modifications allowed

- decisione definitiva ! no shuflling

NON SI POSSONO SPOSTARE I PODS DA UN NODO ALL'ALTRO!
Soluzioni:

- scale up/scale down per evitare disruption
  provato ma:

  - race condition
  - come capire di quale pod richiedere l'eviction
- cordon nodi
- eviction + webhook

#### Plugins

- you can't run plugins on EKS without running a second scheduler

NO Metrics based schedulers

Management of Pods Deployments/ReplicaSets

### Bin packing / repacking

NP hard

First fit
Best fit

Kubernetes LeastAllocated + MostAllocated

### System architecture

simulatore / calcolatore stato + attuatore

per la rimozione fisica dei nodi si basa sul cluster autoscaler

webhook, controller, planner
how to share data among different components?

#### Planner

per evitare troppa disruption limitiamo il calcolo ad una porzione del cluster cercando di minimizzare rispettando i vincoli dati in configurazione

scelta nodi non deterministica + deterministica

idealmente gira come cron job od ogni volta che un pod non entra nello stato attuale del cluster e rimane in pending. Conflitto con cluster autoscaler!

utilizzo strumenti cluster autoscaler (snapshot)
il simulatore dell'autoscaler fa solo fase di filter!

utilizza scheduler simulatore reale

diverse permutazioni dei pod (by cpu, by memory, random) e re-scheduling

informers

##### configurazione

configmap

regole:

- pesi/regole
- nodi fissi

regex

ignora daemonSets, jobs, pod di sistema, namespace di sistema

#### CRD + Controller

New CRDs:

- CondolidationPlan
- PodMove

Kubebuilder controller

Idempotenza

A new pods triggers the cancellation of the plan

maxConcurrentReconciles = 1

se il controller va giù non succede niente

#### Mutating admission Webhook

per battere lo scheduler su selezione del nodo

controllare sideEffects

verificare che il pod sia persistito

Per i Deployment o ReplicaSet non mi serve sapere quale era il nodo di partenza, mi basta solo sapere quello finale
Per gli StatefulSet lo devo sapere ma posso fare il match tramite il nome

availability webhook
multiple replicas
se il webhook va giù i pod vengono rischedulati lo stesso dallo scheduler di default

### Tests & results

Testbeds:

- Kind
- KWOK
- EKS

Deploy via an Helm Chart

Results analysis

### Possible improvements

- salvare stato nodi
    se arriva un nuovo pod calcolare se può andare bene con il piano simulato e nel caso ok altrimenti stoppa tutto
