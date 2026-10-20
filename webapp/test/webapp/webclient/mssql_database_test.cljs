(ns webapp.webclient.mssql-database-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.webclient.mssql-database :as mssql-database]))

(def ^:private user-script "SELECT * FROM dbo.t")

;; One line per server language, as USE printed it through the gateway and agent.
(deftest drops-the-database-context-line
  (doseq [line ["Changed database context to 'sales-db'."
                "Changed database context to 'sales-db'.\r"
                "Der Datenbankkontext wurde in \"sales-db\" geändert."
                "Le contexte de la base de données a été modifié en 'sales-db'."
                "データベース コンテキストが 'sales-db' に変更されました。"
                "Databasekonteksten blev ændret til 'sales-db'."
                "Se cambió el contexto de la base de datos a 'sales-db'."
                "Il contesto di database è stato sostituito con 'sales-db'."
                "De databasecontext is gewijzigd in sales-db."
                "O contexto da base de dados foi alterado para 'sales-db'."
                "Tietokannan kontekstiksi muutettiin sales-db."
                "Ändrade databasens kontext till sales-db."
                "Kontext databáze byl změněn na sales-db."
                "Adatbázis-környezet módosítva a következő értékre: „sales-db”."
                "Zmieniono kontekst bazy danych na „sales-db”."
                "Το περιβάλλον της βάσης δεδομένων άλλαξε σε 'sales-db'."
                "Контекст базы данных изменен на \"sales-db\"."
                "Veritabanı bağlamı 'sales-db' olarak değiştirildi."
                "Contexto do banco de dados alterado para 'sales-db'."
                "已將資料庫內容變更為 'sales-db'。"
                "데이터베이스 컨텍스트가 'sales-db'(으)로 변경되었습니다."
                "已将数据库上下文更改为 \"sales-db\"。"
                "Endret databasekontekst til sales-db."]]
    (is (= "id\n--\n3\n" (mssql-database/strip-output (str line "\nid\n--\n3\n")))
        line)))

(deftest keeps-the-error
  (let [output "Incorrect syntax near '-'.\n\nfailed executing command, reason=exit status 1"]
    (is (= output (mssql-database/strip-output output)))))

(deftest keeps-the-header
  (let [output "id\n--\n3\n\n(1 row affected)\n"]
    (is (= output (mssql-database/strip-output output)))))

(deftest keeps-an-error-that-arrives-before-the-context-line
  (let [output "Invalid object name 'dbo.nope'.\n\nChanged database context to 'sales-db'.\n"]
    (is (= output (mssql-database/strip-output output)))))

(deftest keeps-a-line-that-only-starts-like-the-context-line
  (let [output "Changed database context to 'sales-db'. Extra\nid\n"]
    (is (= output (mssql-database/strip-output output)))))

(deftest missing-output-stays-missing
  (is (nil? (mssql-database/strip-output nil))))

(deftest terminal-script-hides-its-prefix
  (is (= user-script (mssql-database/strip-script
                      (mssql-database/with-database "sales-db" user-script))))
  (is (= user-script (mssql-database/strip-script user-script))))
