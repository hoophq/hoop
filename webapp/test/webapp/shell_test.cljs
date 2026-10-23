(ns webapp.shell-test
  (:require
   [cljs.test :refer [deftest is testing use-fixtures]]
   [webapp.shell :as shell]))

(use-fixtures :each
  {:after (fn []
            (js-delete js/window "__hoopReactShellPresent")
            (.removeItem js/localStorage "react-shell"))})

(deftest react-shell-follows-window-flag
  (testing "no flag: standalone mode"
    (is (false? (shell/react-shell?))))
  (testing "flag set by the shell: hosted mode"
    (set! (.-__hoopReactShellPresent js/window) true)
    (is (true? (shell/react-shell?)))))

(deftest react-shell-ignores-shared-storage
  (testing "another tab setting or clearing the key changes nothing"
    (set! (.-__hoopReactShellPresent js/window) true)
    (.removeItem js/localStorage "react-shell")
    (is (true? (shell/react-shell?)))
    (js-delete js/window "__hoopReactShellPresent")
    (.setItem js/localStorage "react-shell" "true")
    (is (false? (shell/react-shell?)))))
