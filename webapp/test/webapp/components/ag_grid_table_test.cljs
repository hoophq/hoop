(ns webapp.components.ag-grid-table-test
  "Command output is ragged — a value holding a tab makes a row wider than the
  header — so these transforms carry the normalization the grid needs."
  (:require
   [cljs.test :refer-macros [deftest testing is]]
   [webapp.components.ag-grid-table :as ag-grid-table]))

(defn- col-defs [headers]
  (js->clj (ag-grid-table/headers->col-defs (clj->js headers)) :keywordize-keys true))

(defn- row-data [headers rows]
  (js->clj (ag-grid-table/rows->row-data (clj->js headers) (clj->js rows))))

(deftest col-defs-carry-the-header-as-field-and-name
  (is (= [{:field "id" :cellEditor "agTextCellEditor" :headerName "id"}
          {:field "name" :cellEditor "agTextCellEditor" :headerName "name"}]
         (col-defs ["id" "name"]))))

(deftest non-string-headers-are-stringified
  (is (= ["1" "2"] (map :field (col-defs [1 2]))))
  (is (= [{"1" "a"}] (row-data [1] [["a"]]))))

(deftest rows-matching-the-header-are-keyed-by-column
  (is (= [{"id" "1" "name" "alice"} {"id" "2" "name" "bob"}]
         (row-data ["id" "name"] [["1" "alice"] ["2" "bob"]]))))

(deftest short-rows-are-padded
  (is (= [{"a" "1" "b" "2" "c" ""} {"a" "1" "b" "" "c" ""}]
         (row-data ["a" "b" "c"] [["1" "2"] ["1"]]))))

(deftest nil-cells-become-empty-strings
  (is (= [{"a" "1" "b" ""}]
         (row-data ["a" "b"] [["1" nil]]))))

(deftest wide-rows-drop-their-empty-cells-first
  (testing "dropping the blanks realigns the rest with the header"
    (is (= [{"a" "1" "b" "2"}]
           (row-data ["a" "b"] [["1" "2" "" ""]])))
    (is (= [{"a" "x" "b" "y"}]
           (row-data ["a" "b"] [["" "x" "" "y"]])))))

(deftest wide-rows-that-stay-wide-are-truncated-to-the-header
  (is (= [{"a" "1" "b" "2"}]
         (row-data ["a" "b"] [["1" "2" "3"]]))))

(deftest wide-rows-shorter-after-filtering-are-padded
  (is (= [{"a" "1" "b" "" "c" ""}]
         (row-data ["a" "b" "c"] [["1" "" "" ""]]))))

(deftest duplicate-header-names-keep-the-last-column
  (is (= [{"a" "2"}]
         (row-data ["a" "a"] [["1" "2"]]))))

(deftest no-rows-produces-no-row-data
  (is (= [] (row-data ["a" "b"] []))))
