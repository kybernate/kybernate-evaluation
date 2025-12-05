# kybernate-restore-pod

Hilfs-CLI, das aus einem bestehenden Pod + Checkpoint-Pfad ein Restore-Pod-Manifest für Restore-Variante B erzeugt.

> Hinweis: Erste, vereinfachte Version – aktuell wird nur ein minimales Manifest mit Restore-Annotation und `RESTORE_FROM`-Env generiert. Für produktive Nutzung solltest du das Pod-Spec weiter an deine Anforderungen anpassen.

## Build

```bash
cd /home/andre/Workspace/kybernate-evaluation/cmd/kybernate-restore-pod
go build -o ../../bin/kybernate-restore-pod .
```

## Verwendung (als Manifest-Generator)

Beispiel: Original-Pod `pytorch-training-pod` im Namespace `ml-training`, Checkpoint unter `/checkpoints/pytorch-training-pod/20251204-193053`:

```bash
./bin/kybernate-restore-pod \
  -namespace ml-training \
  -pod pytorch-training-pod \
  -checkpoint /checkpoints/pytorch-training-pod/20251204-193053 \
  > restore-pod.yaml

kubectl apply -f restore-pod.yaml
```

Das Manifest enthält `kybernate.io/restore-from` sowie eine `RESTORE_FROM`-Env-Variable und kann so direkt von `kybernate-runtime` genutzt werden.
