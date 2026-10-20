(ns webapp.webclient.mssql-database
  (:require [clojure.string :as cs]
            [goog.string :as gstring]))

(def ^:private prefix-start "SET NOCOUNT ON;\nUSE [")

(defn with-database [database script]
  (str prefix-start database "];\n" script))

(defn- with-database? [script]
  (and (string? script) (cs/starts-with? script prefix-start)))

(defn strip-script [script]
  (if (with-database? script)
    (cs/join "\n" (drop 2 (cs/split script #"\n")))
    script))

;; Message 5701, printed by USE in the login's language: every translation in
;; sys.messages on SQL Server 2022, with %s for the database name.
(def ^:private database-context-messages
  ["Changed database context to '%s'."
   "已將資料庫內容變更為 '%s'。"
   "Kontext databáze byl změněn na %s."
   "Databasekonteksten blev ændret til '%s'."
   "Der Datenbankkontext wurde in \"%s\" geändert."
   "Το περιβάλλον της βάσης δεδομένων άλλαξε σε '%s'."
   "Tietokannan kontekstiksi muutettiin %s."
   "Le contexte de la base de données a été modifié en '%s'."
   "Adatbázis-környezet módosítva a következő értékre: „%s”."
   "Il contesto di database è stato sostituito con '%s'."
   "データベース コンテキストが '%s' に変更されました。"
   "데이터베이스 컨텍스트가 '%s'(으)로 변경되었습니다."
   "De databasecontext is gewijzigd in %s."
   "Endret databasekontekst til %s."
   "Zmieniono kontekst bazy danych na „%s”."
   "Contexto do banco de dados alterado para '%s'."
   "Контекст базы данных изменен на \"%s\"."
   "Ändrade databasens kontext till %s."
   "Veritabanı bağlamı '%s' olarak değiştirildi."
   "已将数据库上下文更改为 \"%s\"。"
   "O contexto da base de dados foi alterado para '%s'."
   "Se cambió el contexto de la base de datos a '%s'."])

(def ^:private database-context-line
  (re-pattern
   (str "^(?:"
        (cs/join "|" (map #(cs/replace (gstring/regExpEscape %) "%s" ".*")
                          database-context-messages))
        ")\\r?$")))

(defn strip-output [output]
  (if-let [idx (and output (cs/index-of output "\n"))]
    (if (re-find database-context-line (subs output 0 idx))
      (subs output (inc idx))
      output)
    output))
