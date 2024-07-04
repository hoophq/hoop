;; (ns webapp.components.codemirror-editor
;;   (:require
;;    [reagent.core :as r]
;;   ;;  ["@codemirror/basic-setup" :as cm]
;;    ))

;; (defn editor
;;   [{:keys [state]}]
;;   (let [!editor-view (atom {:current nil})
;;         editor-state (state)
;;         lambda-view #(new cm/EditorView
;;                           (clj->js {:state editor-state
;;                                     :parent %}))
;;         component (fn []
;;                     (r/create-class
;;                      {:display-name "codemirror-editor"
;;                       :component-did-mount
;;                       (fn []
;;                         (swap! !editor-view assoc :current (lambda-view (:current @!editor-view))))
;;                       :reagent-render
;;                       (fn []
;;                         [:div {:class "overflow-scroll border h-full"
;;                                :ref #(swap! !editor-view assoc :current %)}])}))]
;;     {:editor-state editor-state
;;      :editor-component component
;;      :editor-view !editor-view}))
